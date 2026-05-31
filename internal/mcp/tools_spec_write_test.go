package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildApproachBody_PopulatesAllFields(t *testing.T) {
	in := proposeApproachInput{
		ID:        "app-feat-login",
		Title:     "Login flow approach",
		Summary:   "JWT-based with refresh tokens",
		ParentID:  "feat-login",
		Body:      "## Implementation\n\nUse JWT...",
		Decisions: []string{"dec-auth-approach"},
		Advances:  []string{"goal-secure-by-default"},
		Respects:  []string{"agoal-fundraising"},
	}
	body, err := buildApproachBody(in, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "app-feat-login", body.ID)
	assert.Equal(t, "Login flow approach", body.Title)
	assert.Equal(t, "feat-login", body.ParentID)
	assert.False(t, body.CreatedAt.IsZero(), "created_at must be stamped when zero passed")
	assert.False(t, body.UpdatedAt.IsZero(), "updated_at must be stamped")
}

func TestBuildApproachBody_PreservesCreatedAtWhenSupplied(t *testing.T) {
	in := proposeApproachInput{
		ID: "app-feat-bar", Title: "Bar", ParentID: "feat-bar", Body: "x",
	}
	original := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body, err := buildApproachBody(in, original)
	require.NoError(t, err)
	assert.Equal(t, original, body.CreatedAt, "created_at must be preserved when non-zero")
	assert.True(t, body.UpdatedAt.After(original) || body.UpdatedAt.Equal(original), "updated_at must be now (>= original)")
}

func TestBuildApproachBody_RejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		in   proposeApproachInput
		want string
	}{
		{"empty id", proposeApproachInput{Title: "T", ParentID: "feat-x", Body: "b"}, "id is required"},
		{"wrong id prefix", proposeApproachInput{ID: "dec-foo", Title: "T", ParentID: "feat-x", Body: "b"}, "app- prefix"},
		{"empty title", proposeApproachInput{ID: "app-x", ParentID: "feat-x", Body: "b"}, "title is required"},
		{"empty parent_id", proposeApproachInput{ID: "app-x", Title: "T", Body: "b"}, "parent_id is required"},
		{"bad parent prefix", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "goal-foo", Body: "b"}, "parent_id must be a feature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildApproachBody(tc.in, time.Time{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// DJ-148 Task 8 — end-to-end approach mutation tests (non-dry-run path).
// Validates that spec_propose_approach persists new fields to disk and
// rejects missing parents per the handler's validation logic.

func TestProposeApproach_PersistsToDisk(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	// seed parent
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{
		ID:        "feat-foo",
		Title:     "Foo",
		Status:    spec.FeatureStatusProposed,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, agent.OriginSettled))

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, reg, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_approach",
		Arguments: map[string]any{
			"id":        "app-feat-foo",
			"title":     "Foo approach",
			"parent_id": "feat-foo",
			"body":      "## Implementation",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "result: %+v", res)

	// file persisted
	data, err := fsys.ReadFile(".borg/spec/approaches/app-feat-foo.md")
	require.NoError(t, err)
	contents := string(data)
	assert.Contains(t, contents, "id: app-feat-foo")
	assert.Contains(t, contents, "title: Foo approach")
	assert.Contains(t, contents, "parent_id: feat-foo")
}

func TestProposeApproach_RejectsMissingParent(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, reg, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_approach",
		Arguments: map[string]any{
			"id":        "app-feat-missing",
			"title":     "Missing parent",
			"parent_id": "feat-missing",
			"body":      "x",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}
