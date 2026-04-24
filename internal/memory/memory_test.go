package memory_test

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/memory"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const testRoot = "memory"

func TestInMemoryAddAndSearch(t *testing.T) {
	svc := memory.NewInMemoryService()
	ctx := context.Background()

	err := svc.AddSessionToMemory(ctx, "archivist", []memory.Entry{
		{Content: "User asked about auth middleware"},
		{Content: "Session tokens stored in Redis"},
		{Content: "Migration plan drafted for session cleanup"},
	})
	require.NoError(t, err)

	resp, err := svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist", Query: "session"})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)

	resp, err = svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist", Query: "Redis"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Contains(t, resp.Entries[0].Content, "Redis")

	resp, err = svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist"})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 3)
}

func TestInMemoryNamespaceIsolation(t *testing.T) {
	svc := memory.NewInMemoryService()
	ctx := context.Background()

	require.NoError(t, svc.AddSessionToMemory(ctx, "archivist", []memory.Entry{
		{Content: "archivist-only content"},
	}))
	require.NoError(t, svc.AddSessionToMemory(ctx, "planner", []memory.Entry{
		{Content: "planner-only content"},
	}))

	resp, err := svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist", Query: "content"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "archivist-only content", resp.Entries[0].Content)

	resp, err = svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "planner", Query: "content"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "planner-only content", resp.Entries[0].Content)

	resp, err = svc.SearchMemory(ctx, &memory.SearchRequest{Query: "content"})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)
}

func TestFileBackedRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	ctx := context.Background()

	svc1 := memory.NewFileStoreService(fsys, testRoot)
	require.NoError(t, svc1.AddSessionToMemory(ctx, "archivist", []memory.Entry{
		{Content: "persisted entry one", Author: "archivist"},
		{Content: "persisted entry two", Author: "archivist"},
	}))

	svc2 := memory.NewFileStoreService(fsys, testRoot)
	resp, err := svc2.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 2)

	var contents []string
	for _, e := range resp.Entries {
		assert.NotEmpty(t, e.ID)
		assert.Equal(t, "archivist", e.Namespace)
		assert.Equal(t, "archivist", e.Author)
		assert.False(t, e.Timestamp.IsZero())
		contents = append(contents, e.Content)
	}
	assert.ElementsMatch(t, []string{"persisted entry one", "persisted entry two"}, contents)
}

func TestFileBackedCorruptEntryIgnored(t *testing.T) {
	fsys := specio.NewMemFS()
	ctx := context.Background()

	svc := memory.NewFileStoreService(fsys, testRoot)
	require.NoError(t, svc.AddSessionToMemory(ctx, "archivist", []memory.Entry{
		{Content: "healthy entry"},
	}))

	require.NoError(t, fsys.MkdirAll(testRoot+"/archivist", 0o755))
	require.NoError(t, fsys.WriteFile(
		testRoot+"/archivist/corrupt.yaml",
		[]byte("id: [unterminated\n  key: :bad"),
		0o644,
	))

	svc2 := memory.NewFileStoreService(fsys, testRoot)
	resp, err := svc2.SearchMemory(ctx, &memory.SearchRequest{Namespace: "archivist"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "healthy entry", resp.Entries[0].Content)
}

func TestAddSessionToMemoryAppendsEvents(t *testing.T) {
	svc := memory.NewInMemoryService()
	ctx := context.Background()

	require.NoError(t, svc.AddSessionToMemory(ctx, "planner", []memory.Entry{
		{Content: "turn-1"},
		{Content: "turn-2"},
	}))
	require.NoError(t, svc.AddSessionToMemory(ctx, "planner", []memory.Entry{
		{Content: "turn-3"},
	}))

	resp, err := svc.SearchMemory(ctx, &memory.SearchRequest{Namespace: "planner"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 3)

	var contents []string
	for _, e := range resp.Entries {
		contents = append(contents, e.Content)
	}
	assert.ElementsMatch(t, []string{"turn-1", "turn-2", "turn-3"}, contents)
}

func TestEntryMetadataRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	ctx := context.Background()

	meta := map[string]any{
		"source":     "refine-council",
		"turn_index": 3,
		"tags":       []any{"assumption", "challenge"},
	}
	svc1 := memory.NewFileStoreService(fsys, testRoot)
	require.NoError(t, svc1.AddSessionToMemory(ctx, "analyst", []memory.Entry{
		{Content: "motivation captured", CustomMetadata: meta},
	}))

	svc2 := memory.NewFileStoreService(fsys, testRoot)
	resp, err := svc2.SearchMemory(ctx, &memory.SearchRequest{Namespace: "analyst"})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	got := resp.Entries[0]
	assert.Equal(t, "refine-council", got.CustomMetadata["source"])
	assert.EqualValues(t, 3, got.CustomMetadata["turn_index"])

	files, err := fsys.ListDir(testRoot + "/analyst")
	require.NoError(t, err)
	require.Len(t, files, 1)
	raw, err := fsys.ReadFile(files[0])
	require.NoError(t, err)
	var rt memory.Entry
	require.NoError(t, yaml.Unmarshal(raw, &rt))
	assert.Equal(t, "motivation captured", rt.Content)
	assert.Equal(t, "refine-council", rt.CustomMetadata["source"])
}
