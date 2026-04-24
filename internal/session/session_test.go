package session_test

import (
	"context"
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/session"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRoot = "workstreams"

func TestInMemoryAppendAndRead(t *testing.T) {
	store := session.NewInMemoryStore()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, store.Append(ctx, session.SessionEvent{
			WorkstreamID: "ws1",
			AgentName:    "drafter",
			Role:         "assistant",
			Content:      fmt.Sprintf("turn-%d", i),
		}))
	}

	got, err := store.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for i, ev := range got {
		assert.Equal(t, fmt.Sprintf("turn-%d", i), ev.Content)
		assert.NotEmpty(t, ev.ID)
		assert.Equal(t, "ws1", ev.WorkstreamID)
		assert.False(t, ev.Timestamp.IsZero())
	}
}

func TestInMemoryWorkstreamIsolation(t *testing.T) {
	store := session.NewInMemoryStore()
	ctx := context.Background()

	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws-A", Role: "user", Content: "A only",
	}))
	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws-B", Role: "user", Content: "B only",
	}))

	a, err := store.Read(ctx, "ws-A", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, a, 1)
	assert.Equal(t, "A only", a[0].Content)

	b, err := store.Read(ctx, "ws-B", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, b, 1)
	assert.Equal(t, "B only", b[0].Content)

	missing, err := store.Read(ctx, "ws-C", session.ReadOpts{})
	require.NoError(t, err)
	assert.Empty(t, missing)
}

func TestFileBackedRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	ctx := context.Background()

	store1 := session.NewFileStore(fsys, testRoot)
	for i := 0; i < 3; i++ {
		require.NoError(t, store1.Append(ctx, session.SessionEvent{
			WorkstreamID: "ws1",
			AgentName:    "drafter",
			Role:         "assistant",
			Content:      fmt.Sprintf("turn-%d", i),
		}))
	}

	store2 := session.NewFileStore(fsys, testRoot)
	got, err := store2.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for i, ev := range got {
		assert.Equal(t, fmt.Sprintf("turn-%d", i), ev.Content)
		assert.Equal(t, "drafter", ev.AgentName)
		assert.NotEmpty(t, ev.ID)
	}
}

func TestFileBackedAppendIsAppendOnly(t *testing.T) {
	fsys := specio.NewMemFS()
	store := session.NewFileStore(fsys, testRoot)
	ctx := context.Background()

	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws1", Role: "user", Content: "first",
	}))

	eventsPath := path.Join(testRoot, "ws1", "events.yaml")
	before, err := fsys.ReadFile(eventsPath)
	require.NoError(t, err)
	require.NotEmpty(t, before)

	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws1", Role: "assistant", Content: "second",
	}))

	after, err := fsys.ReadFile(eventsPath)
	require.NoError(t, err)

	assert.Greater(t, len(after), len(before), "file must grow after append")
	assert.Equal(t, before, after[:len(before)], "prefix must be byte-for-byte preserved")
}

func TestFileBackedCorruptTailIgnored(t *testing.T) {
	fsys := specio.NewMemFS()
	store := session.NewFileStore(fsys, testRoot)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		require.NoError(t, store.Append(ctx, session.SessionEvent{
			WorkstreamID: "ws1", Role: "user", Content: fmt.Sprintf("ev-%d", i),
		}))
	}

	eventsPath := path.Join(testRoot, "ws1", "events.yaml")
	data, err := fsys.ReadFile(eventsPath)
	require.NoError(t, err)

	corrupted := append(data, []byte("---\nid: [unterminated\n  content: [\n")...)
	require.NoError(t, fsys.WriteFile(eventsPath, corrupted, 0o644))

	store2 := session.NewFileStore(fsys, testRoot)
	got, err := store2.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, got, 2, "pre-corruption events must survive")
	assert.Equal(t, "ev-0", got[0].Content)
	assert.Equal(t, "ev-1", got[1].Content)
}

func TestReadRangeFiltersByTime(t *testing.T) {
	store := session.NewInMemoryStore()
	ctx := context.Background()

	base := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		require.NoError(t, store.Append(ctx, session.SessionEvent{
			WorkstreamID: "ws1",
			Role:         "user",
			Content:      fmt.Sprintf("ev-%d", i),
			Timestamp:    base.Add(time.Duration(i*6) * time.Second),
		}))
	}

	got, err := store.Read(ctx, "ws1", session.ReadOpts{Since: base.Add(30 * time.Second)})
	require.NoError(t, err)
	assert.Len(t, got, 5, "Since is inclusive; 30s,36s,42s,48s,54s qualify")

	got, err = store.Read(ctx, "ws1", session.ReadOpts{Until: base.Add(18 * time.Second)})
	require.NoError(t, err)
	assert.Len(t, got, 4, "Until is inclusive; 0s,6s,12s,18s qualify")

	got, err = store.Read(ctx, "ws1", session.ReadOpts{
		Since: base.Add(12 * time.Second),
		Until: base.Add(30 * time.Second),
	})
	require.NoError(t, err)
	assert.Len(t, got, 4, "both bounds inclusive; 12s,18s,24s,30s qualify")

	got, err = store.Read(ctx, "ws1", session.ReadOpts{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestParentEventIDRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	store := session.NewFileStore(fsys, testRoot)
	ctx := context.Background()

	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws1",
		AgentName:    "drafter",
		Role:         "assistant",
		Content:      "proposed approach X",
	}))

	got, err := store.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	parentID := got[0].ID
	require.NotEmpty(t, parentID)

	require.NoError(t, store.Append(ctx, session.SessionEvent{
		WorkstreamID: "ws1",
		AgentName:    "challenger",
		Role:         "user",
		Content:      "strategy Y rules that out",
		ParentID:     parentID,
	}))

	store2 := session.NewFileStore(fsys, testRoot)
	got, err = store2.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Empty(t, got[0].ParentID)
	assert.Equal(t, parentID, got[1].ParentID)
}

func TestConcurrentAppendSafe(t *testing.T) {
	fsys := specio.NewMemFS()
	store := session.NewFileStore(fsys, testRoot)
	ctx := context.Background()

	const perGoroutine = 25
	var wg sync.WaitGroup
	wg.Add(2)
	for _, tag := range []string{"a", "b"} {
		tag := tag
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := store.Append(ctx, session.SessionEvent{
					WorkstreamID: "ws1",
					Role:         "user",
					Content:      fmt.Sprintf("%s-%d", tag, i),
				})
				if err != nil {
					t.Errorf("append failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := store.Read(ctx, "ws1", session.ReadOpts{})
	require.NoError(t, err)
	assert.Len(t, got, 2*perGoroutine, "no events lost under concurrent append")

	ids := make(map[string]struct{}, len(got))
	for _, ev := range got {
		ids[ev.ID] = struct{}{}
	}
	assert.Len(t, ids, 2*perGoroutine, "ids are unique")
}
