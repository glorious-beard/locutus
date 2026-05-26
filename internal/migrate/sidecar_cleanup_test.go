package migrate

import (
	"sort"
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupSpecSidecars_RemovesSidecarsWithMatchingJSONTwin(t *testing.T) {
	fsys := specio.NewMemFS()
	// Decision: both .json + .md present (the sidecar pattern).
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-storage.json", []byte(`{"id":"dec-storage"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-storage.md", []byte("---\nid: dec-storage\n---\n"), 0o644))
	// Feature: same pattern.
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/features/feat-x.json", []byte(`{"id":"feat-x"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/features/feat-x.md", []byte("---\nid: feat-x\n---\n"), 0o644))

	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	sort.Strings(result.Removed)
	assert.Equal(t, []string{".borg/spec/decisions/dec-storage.md", ".borg/spec/features/feat-x.md"}, result.Removed)
	assert.Equal(t, 2, result.Scanned)

	// Confirm the .md files are gone but the .json bodies remain.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-storage.md")
	assert.Error(t, err, "sidecar should be removed")
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-storage.json")
	assert.NoError(t, err, "JSON body must survive")
}

func TestCleanupSpecSidecars_LeavesMDWithoutJSONTwinAlone(t *testing.T) {
	// Defensive: if an operator somehow placed a hand-authored
	// markdown under one of these directories with no JSON sibling,
	// we don't delete their work — only matching pairs.
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/manual-notes.md", []byte("hand-written notes\n"), 0o644))

	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Empty(t, result.Removed, "orphan .md without .json twin must not be removed")
	assert.Equal(t, 1, result.Scanned, "orphan was scanned but not removed")

	_, err = fsys.ReadFile(".borg/spec/decisions/manual-notes.md")
	assert.NoError(t, err, "orphan markdown must survive")
}

func TestCleanupSpecSidecars_PreservesSidecarWithBodyContent(t *testing.T) {
	// Strategy sidecars may carry the strategy's prose body (legacy
	// pre-DJ-135 authoring path). Cleanup must NOT delete those.
	// Decisions/features/bugs always have frontmatter-only sidecars
	// in practice (their narrative lives on the typed struct);
	// they're the ones cleanup is meant to remove.
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))
	// Strategy with substantive body content.
	require.NoError(t, fsys.WriteFile(".borg/spec/strategies/strat-with-body.json", []byte(`{"id":"strat-with-body"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/strategies/strat-with-body.md",
		[]byte("---\nid: strat-with-body\n---\n\nUse PostgreSQL 16 with PostGIS for geospatial queries.\nDeploy via Cloud Run with RDS Multi-AZ.\n"), 0o644))
	// Strategy with empty body (frontmatter-only — the typical
	// post-DJ-135 case where strategies are authored via MCP and
	// have no body content).
	require.NoError(t, fsys.WriteFile(".borg/spec/strategies/strat-empty.json", []byte(`{"id":"strat-empty"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/strategies/strat-empty.md",
		[]byte("---\nid: strat-empty\n---\n"), 0o644))

	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)

	// Only the empty-body sidecar is removed.
	assert.Equal(t, []string{".borg/spec/strategies/strat-empty.md"}, result.Removed)
	assert.Equal(t, 2, result.Scanned)

	// The body-bearing strategy sidecar survives.
	_, err = fsys.ReadFile(".borg/spec/strategies/strat-with-body.md")
	assert.NoError(t, err, "sidecar with substantive body must be preserved")
}

func TestCleanupSpecSidecars_LeavesApproachesAlone(t *testing.T) {
	// spec.Approach stores its canonical body as markdown via
	// SaveMarkdown; those .md files under .borg/spec/approaches/
	// are load-bearing, not sidecars. Cleanup must not touch them.
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/approaches/app-x.md", []byte("---\nid: app-x\n---\n\n# Body\n"), 0o644))

	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Empty(t, result.Removed, "approach .md must not be touched by sidecar cleanup")
	assert.Equal(t, 0, result.Scanned, "approaches directory must not even be scanned")

	_, err = fsys.ReadFile(".borg/spec/approaches/app-x.md")
	assert.NoError(t, err, "approach .md must survive (it's the canonical body)")
}

func TestCleanupSpecSidecars_IsIdempotent(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-a.json", []byte(`{"id":"dec-a"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-a.md", []byte("---\nid: dec-a\n---\n"), 0o644))

	// First run removes the sidecar.
	first, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Len(t, first.Removed, 1)

	// Second run finds nothing to remove and returns clean.
	second, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Empty(t, second.Removed, "second run must be a no-op")
	assert.Equal(t, 0, second.Scanned)
}

func TestCleanupSpecSidecars_MissingDirectoriesAreNoOp(t *testing.T) {
	// A fresh project with no .borg/spec/ at all should not error.
	fsys := specio.NewMemFS()
	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Empty(t, result.Removed)
}

func TestCleanupSpecSidecars_CoversAllFourSidecarKinds(t *testing.T) {
	fsys := specio.NewMemFS()
	for _, dir := range []string{
		".borg/spec/decisions",
		".borg/spec/features",
		".borg/spec/strategies",
		".borg/spec/bugs",
	} {
		require.NoError(t, fsys.MkdirAll(dir, 0o755))
		require.NoError(t, fsys.WriteFile(dir+"/x.json", []byte(`{"id":"x"}`), 0o644))
		require.NoError(t, fsys.WriteFile(dir+"/x.md", []byte("---\nid: x\n---\n"), 0o644))
	}

	result, err := CleanupSpecSidecars(fsys)
	require.NoError(t, err)
	assert.Len(t, result.Removed, 4, "all four sidecar-bearing kinds must be cleaned")
}
