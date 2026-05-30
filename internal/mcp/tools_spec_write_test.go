package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildApproachBody_PopulatesAllFields(t *testing.T) {
	in := proposeApproachInput{
		ID:          "app-feat-login",
		Title:       "Login flow approach",
		Summary:     "JWT-based with refresh tokens",
		ParentID:    "feat-login",
		Body:        "## Implementation\n\nUse JWT...",
		SourceFiles: []string{"internal/auth/jwt.go", "internal/auth/middleware.go"},
		SourceHash:  "sha256:deadbeef",
		Decisions:   []string{"dec-auth-approach"},
		Advances:    []string{"goal-secure-by-default"},
		Respects:    []string{"agoal-fundraising"},
	}
	body, err := buildApproachBody(in, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "app-feat-login", body.ID)
	assert.Equal(t, "Login flow approach", body.Title)
	assert.Equal(t, "feat-login", body.ParentID)
	assert.Equal(t, []string{"internal/auth/jwt.go", "internal/auth/middleware.go"}, body.SourceFiles)
	assert.Equal(t, "sha256:deadbeef", body.SourceHash)
	assert.False(t, body.CreatedAt.IsZero(), "created_at must be stamped when zero passed")
	assert.False(t, body.UpdatedAt.IsZero(), "updated_at must be stamped")
	assert.False(t, body.SourceHashSyncedAt.IsZero(), "source_hash_synced_at must be stamped")
}

func TestBuildApproachBody_PreservesCreatedAtWhenSupplied(t *testing.T) {
	in := proposeApproachInput{
		ID: "app-feat-bar", Title: "Bar", ParentID: "feat-bar", Body: "x",
		SourceFiles: []string{"f.go"}, SourceHash: "sha256:x",
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
		{"empty id", proposeApproachInput{Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:xxxxxxxx"}, "id is required"},
		{"wrong id prefix", proposeApproachInput{ID: "dec-foo", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:xxxxxxxx"}, "app- prefix"},
		{"empty title", proposeApproachInput{ID: "app-x", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:xxxxxxxx"}, "title is required"},
		{"empty parent_id", proposeApproachInput{ID: "app-x", Title: "T", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:xxxxxxxx"}, "parent_id is required"},
		{"bad parent prefix", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "goal-foo", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:xxxxxxxx"}, "parent_id must be a feature"},
		{"empty source_files", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceHash: "sha256:xxxxxxxx"}, "source_files is required"},
		{"empty source_hash", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}}, "source_hash is required"},
		{"malformed source_hash", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "abc"}, "source_hash must be in sha256:<hex>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildApproachBody(tc.in, time.Time{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
