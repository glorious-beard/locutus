package specio_test

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sidecarTestNode struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
}

// TestSavePair_EmptyBodyWritesNoSidecar locks in the post-DJ-135
// behavior: when SavePair is called with body="", no .md sidecar is
// written. This is the path that decisions/features/bugs follow now
// that all narrative lives on the typed JSON struct.
func TestSavePair_EmptyBodyWritesNoSidecar(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))

	err := specio.SavePair(fsys, ".borg/spec/decisions/dec-storage",
		sidecarTestNode{ID: "dec-storage", Title: "Choose Postgres", Status: "active"}, "")
	require.NoError(t, err)

	// JSON is the canonical form and must always land.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-storage.json")
	assert.NoError(t, err, "JSON must be written")

	// Sidecar must NOT be written for empty body.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-storage.md")
	assert.Error(t, err, "no .md sidecar when body is empty (post-DJ-135 behavior)")
}

// TestSavePair_NonEmptyBodyWritesSidecar locks in the strategy path:
// when SavePair is called with a non-empty body, the .md sidecar
// carries it (since spec.Strategy has no Body field on the typed
// struct; the prose lives in the .md).
func TestSavePair_NonEmptyBodyWritesSidecar(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))

	err := specio.SavePair(fsys, ".borg/spec/strategies/strat-foundation",
		sidecarTestNode{ID: "strat-foundation", Title: "Foundation", Status: "active"},
		"Foundation strategy body content.")
	require.NoError(t, err)

	mdData, err := fsys.ReadFile(".borg/spec/strategies/strat-foundation.md")
	require.NoError(t, err, ".md sidecar must be written when body is non-empty")
	assert.Contains(t, string(mdData), "Foundation strategy body content.")
}

// TestSavePair_EmptyBodyRemovesStaleSidecar covers the migration
// path: if a .md sidecar exists from a prior build, calling SavePair
// with body="" removes the stale sidecar so the on-disk state
// matches the new convention. Idempotent — second call is a no-op.
func TestSavePair_EmptyBodyRemovesStaleSidecar(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	// Pre-existing sidecar from a prior build.
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-old.md",
		[]byte("---\nid: dec-old\n---\n"), 0o644))

	// First SavePair with empty body removes the stale sidecar.
	err := specio.SavePair(fsys, ".borg/spec/decisions/dec-old",
		sidecarTestNode{ID: "dec-old", Title: "Old decision"}, "")
	require.NoError(t, err)
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-old.md")
	assert.Error(t, err, "stale sidecar must be removed")

	// Second SavePair with empty body is a no-op (sidecar already gone).
	err = specio.SavePair(fsys, ".borg/spec/decisions/dec-old",
		sidecarTestNode{ID: "dec-old", Title: "Old decision"}, "")
	require.NoError(t, err, "remove of missing sidecar must not error")
}
