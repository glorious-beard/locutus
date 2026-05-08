package cmd

import (
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArchiveAbandonedNodes_MovesFilesToArchiveRoot confirms the
// behavior the post-refine cleanup needs: for each abandoned ID,
// move both the .json and .md file out of the active spec graph
// into the archive root, preserving the per-type subdirectory.
// Source files are removed; archive files exist; active graph is
// clean.
func TestArchiveAbandonedNodes_MovesFilesToArchiveRoot(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/features/feat-old.json", []byte(`{"id":"feat-old"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/features/feat-old.md", []byte("---\nid: feat-old\n---\nold body"), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-old.json", []byte(`{"id":"dec-old"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-old.md", []byte("---\nid: dec-old\n---\nold rationale"), 0o644))

	abandoned := []agent.SpecChange{
		{Kind: agent.SpecChangeAbandoned, Type: "feature", ID: "feat-old", Title: "Old feature"},
		{Kind: agent.SpecChangeAbandoned, Type: "decision", ID: "dec-old", Title: "Old decision"},
	}

	archiveRoot := ".borg/spec/.archived/20260507-200000"
	archivedIDs := archiveAbandonedNodes(fsys, abandoned, archiveRoot)

	assert.ElementsMatch(t, []string{"feat-old", "dec-old"}, archivedIDs)

	// Source files gone.
	for _, src := range []string{
		".borg/spec/features/feat-old.json",
		".borg/spec/features/feat-old.md",
		".borg/spec/decisions/dec-old.json",
		".borg/spec/decisions/dec-old.md",
	} {
		_, err := fsys.Stat(src)
		assert.Error(t, err, "source file %q should be removed", src)
	}

	// Archive files present at the expected paths.
	for _, dst := range []string{
		archiveRoot + "/features/feat-old.json",
		archiveRoot + "/features/feat-old.md",
		archiveRoot + "/decisions/dec-old.json",
		archiveRoot + "/decisions/dec-old.md",
	} {
		_, err := fsys.Stat(dst)
		assert.NoError(t, err, "archive file %q should exist", dst)
	}
}

// TestArchiveAbandonedNodes_NoAbandonsIsNoOp confirms the cleanup
// is safe to call on a refine with no abandons (greenfield, or a
// purely-additive run). No archive directory is created if there's
// nothing to archive.
func TestArchiveAbandonedNodes_NoAbandonsIsNoOp(t *testing.T) {
	fsys := specio.NewMemFS()
	archived := archiveAbandonedNodes(fsys, nil, ".borg/spec/.archived/never-created")
	assert.Empty(t, archived)
	_, err := fsys.Stat(".borg/spec/.archived/never-created")
	assert.Error(t, err, "no archive root should be created when there's nothing to archive")
}

// TestArchiveAbandonedNodes_TolerantOfMissingMarkdown confirms the
// cleanup handles partial files gracefully: if only the .json
// exists (no .md), the archive picks up what's there and continues
// without erroring. A spec graph in a partially-saved state
// shouldn't block cleanup.
func TestArchiveAbandonedNodes_TolerantOfMissingMarkdown(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/strategies/strat-half.json", []byte(`{"id":"strat-half"}`), 0o644))
	// Note: no .md file written.

	archiveRoot := ".borg/spec/.archived/20260507-201500"
	archivedIDs := archiveAbandonedNodes(fsys, []agent.SpecChange{
		{Kind: agent.SpecChangeAbandoned, Type: "strategy", ID: "strat-half", Title: "Half"},
	}, archiveRoot)

	assert.ElementsMatch(t, []string{"strat-half"}, archivedIDs)
	_, err := fsys.Stat(".borg/spec/strategies/strat-half.json")
	assert.Error(t, err)
	_, err = fsys.Stat(archiveRoot + "/strategies/strat-half.json")
	assert.NoError(t, err)
}
