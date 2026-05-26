package scaffold_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/glorious-beard/locutus/internal/scaffold"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlaybookOverlay_ResolvesRuntimeSpecificFirst — given both the
// default and a runtime-specific overlay, the overlay wins for that
// runtime; other runtimes get the default.
func TestPlaybookOverlay_ResolvesRuntimeSpecificFirst(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":             {Data: []byte("DEFAULT BODY")},
		"plans/spec_refinement.claude-code.md": {Data: []byte("CLAUDE OVERLAY")},
	}

	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code")
	require.NoError(t, err)
	assert.Equal(t, "CLAUDE OVERLAY", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src)

	// codex has no overlay → falls back to default.
	body, src, err = scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "codex")
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT BODY", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestPlaybookOverlay_FallsBackToDefault — overlay absent → returns
// default. Distinct from the above test in that NO overlay file exists
// for any runtime here.
func TestPlaybookOverlay_FallsBackToDefault(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md": {Data: []byte("ONLY DEFAULT")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code")
	require.NoError(t, err)
	assert.Equal(t, "ONLY DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestPlaybookOverlay_DefaultRequired — both absent → error mentioning
// the canonical path so operators can author or restore it.
func TestPlaybookOverlay_DefaultRequired(t *testing.T) {
	base := fstest.MapFS{}
	_, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code")
	require.Error(t, err)
	assert.Equal(t, "plans/spec_refinement.md", src)
	assert.Contains(t, err.Error(), "plans/spec_refinement.md")
	assert.Contains(t, strings.ToLower(err.Error()), "missing")
}

// TestPlaybookOverlay_EmptyRuntimeReadsDefault — an empty runtime
// string skips the overlay attempt entirely (no synthesized "..md"
// probe) and reads the default.
func TestPlaybookOverlay_EmptyRuntimeReadsDefault(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md": {Data: []byte("DEFAULT")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "")
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}
