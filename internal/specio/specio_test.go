package specio

import (
	"io"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemFSReadWriteFile(t *testing.T) {
	mfs := NewMemFS()
	data := []byte("hello, locutus")

	err := mfs.WriteFile("test.txt", data, 0o644)
	assert.NoError(t, err)

	got, err := mfs.ReadFile("test.txt")
	assert.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestMemFSMkdirAll(t *testing.T) {
	mfs := NewMemFS()

	err := mfs.MkdirAll("a/b/c", 0o755)
	assert.NoError(t, err)

	// Each segment should be stat-able as a directory.
	for _, dir := range []string{"a", "a/b", "a/b/c"} {
		info, err := mfs.Stat(dir)
		assert.NoError(t, err, "Stat(%q)", dir)
		assert.True(t, info.IsDir(), "expected %q to be a directory", dir)
	}
}

func TestMemFSRemove(t *testing.T) {
	mfs := NewMemFS()

	err := mfs.WriteFile("gone.txt", []byte("ephemeral"), 0o644)
	assert.NoError(t, err)

	err = mfs.Remove("gone.txt")
	assert.NoError(t, err)

	_, err = mfs.ReadFile("gone.txt")
	assert.Error(t, err)
}

func TestMemFSOpen(t *testing.T) {
	mfs := NewMemFS()
	content := []byte("open me")

	err := mfs.WriteFile("readable.txt", content, 0o644)
	assert.NoError(t, err)

	f, err := mfs.Open("readable.txt")
	assert.NoError(t, err)
	defer f.Close()

	got, err := io.ReadAll(f)
	assert.NoError(t, err)
	assert.Equal(t, content, got)
}

func newTestDecision(id, title string) spec.Decision {
	return spec.Decision{
		ID:         id,
		Title:      title,
		Status:     spec.DecisionStatusActive,
		Confidence: 0.95,
		Rationale:  "Go is well-suited for CLI tools",
		CreatedAt:  time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
	}
}

func TestSavePairAndLoadPair(t *testing.T) {
	mfs := NewMemFS()
	err := mfs.MkdirAll("decisions", 0o755)
	assert.NoError(t, err)

	original := newTestDecision("d-001", "Backend Language")
	body := "Go was selected for its strong concurrency model."

	err = SavePair[spec.Decision](mfs, "decisions/d-001", original, body)
	assert.NoError(t, err)

	// Verify both sidecar files exist.
	_, err = mfs.ReadFile("decisions/d-001.json")
	assert.NoError(t, err, ".json sidecar should exist")

	_, err = mfs.ReadFile("decisions/d-001.md")
	assert.NoError(t, err, ".md sidecar should exist")

	// Load the pair back and verify fields.
	loaded, loadedBody, err := LoadPair[spec.Decision](mfs, "decisions/d-001")
	assert.NoError(t, err)

	assert.Equal(t, original.ID, loaded.ID)
	assert.Equal(t, original.Title, loaded.Title)
	assert.Equal(t, original.Status, loaded.Status)
	assert.InDelta(t, original.Confidence, loaded.Confidence, 0.001)
	assert.Equal(t, original.Rationale, loaded.Rationale)
	assert.True(t, original.CreatedAt.Equal(loaded.CreatedAt))
	assert.True(t, original.UpdatedAt.Equal(loaded.UpdatedAt))
	assert.Contains(t, loadedBody, "Go was selected")
}

func TestWalkPairs(t *testing.T) {
	mfs := NewMemFS()
	err := mfs.MkdirAll("decisions", 0o755)
	assert.NoError(t, err)

	d1 := newTestDecision("d-001", "Backend Language")
	d2 := newTestDecision("d-002", "Database Choice")

	err = SavePair[spec.Decision](mfs, "decisions/d-001", d1, "Body one.")
	assert.NoError(t, err)
	err = SavePair[spec.Decision](mfs, "decisions/d-002", d2, "Body two.")
	assert.NoError(t, err)

	results, err := WalkPairs[spec.Decision](mfs, "decisions")
	assert.NoError(t, err)
	assert.Len(t, results, 2)

	// Results are sorted by path, so d-001 comes first.
	assert.Equal(t, "d-001", results[0].Object.ID)
	assert.Equal(t, "d-002", results[1].Object.ID)
	assert.NoError(t, results[0].Err)
	assert.NoError(t, results[1].Err)
}

func TestFindOrphans(t *testing.T) {
	mfs := NewMemFS()
	err := mfs.MkdirAll("decisions", 0o755)
	assert.NoError(t, err)

	// d-001: matched pair
	err = mfs.WriteFile("decisions/d-001.json", []byte(`{"id":"d-001"}`), 0o644)
	assert.NoError(t, err)
	err = mfs.WriteFile("decisions/d-001.md", []byte("---\nid: d-001\n---\n"), 0o644)
	assert.NoError(t, err)

	// d-002: json only (orphan)
	err = mfs.WriteFile("decisions/d-002.json", []byte(`{"id":"d-002"}`), 0o644)
	assert.NoError(t, err)

	// d-003: md only (orphan)
	err = mfs.WriteFile("decisions/d-003.md", []byte("---\nid: d-003\n---\n"), 0o644)
	assert.NoError(t, err)

	jsonOnly, mdOnly, err := FindOrphans(mfs, "decisions")
	assert.NoError(t, err)

	assert.Equal(t, []string{"decisions/d-002.json"}, jsonOnly)
	assert.Equal(t, []string{"decisions/d-003.md"}, mdOnly)
}

func TestSaveLoadMarkdown(t *testing.T) {
	mfs := NewMemFS()
	require.NoError(t, mfs.MkdirAll(".borg/spec/approaches", 0o755))

	orig := spec.Approach{
		ID:            "app-oauth",
		Title:         "OAuth via PKCE",
		ParentID:      "feat-auth",
		ArtifactPaths: []string{"src/auth/oauth.go"},
		Decisions:     []string{"dec-use-oauth"},
		Skills:        []string{"go-testing"},
		Prerequisites: []string{"buf"},
		CreatedAt:     time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
	}
	body := "## What to build\n\nImplement OAuth2 PKCE flow.\n"

	require.NoError(t, SaveMarkdown(mfs, ".borg/spec/approaches/app-oauth.md", orig, body))

	gotObj, gotBody, err := LoadMarkdown[spec.Approach](mfs, ".borg/spec/approaches/app-oauth.md")
	require.NoError(t, err)

	assert.Equal(t, orig.ID, gotObj.ID)
	assert.Equal(t, orig.Title, gotObj.Title)
	assert.Equal(t, orig.ParentID, gotObj.ParentID)
	assert.Equal(t, orig.ArtifactPaths, gotObj.ArtifactPaths)
	assert.Equal(t, orig.Decisions, gotObj.Decisions)
	assert.Equal(t, orig.Skills, gotObj.Skills)
	assert.Equal(t, orig.Prerequisites, gotObj.Prerequisites)
	assert.Equal(t, orig.CreatedAt.UTC(), gotObj.CreatedAt.UTC())
	assert.Equal(t, body, gotBody)
}

func TestLoadMarkdownMissingFile(t *testing.T) {
	mfs := NewMemFS()
	_, _, err := LoadMarkdown[spec.Approach](mfs, "nonexistent.md")
	assert.Error(t, err)
}
