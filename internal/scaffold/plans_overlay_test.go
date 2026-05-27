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
// runtime; other runtimes get the default. (Headless mode preserves
// the old two-tier provider-overlay → default behavior.)
func TestPlaybookOverlay_ResolvesRuntimeSpecificFirst(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":             {Data: []byte("DEFAULT BODY")},
		"plans/spec_refinement.claude-code.md": {Data: []byte("CLAUDE OVERLAY")},
	}

	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "CLAUDE OVERLAY", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src)

	// codex has no overlay → falls back to default.
	body, src, err = scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "codex", scaffold.ModeHeadless)
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
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "ONLY DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestPlaybookOverlay_DefaultRequired — both absent → error mentioning
// the canonical path so operators can author or restore it.
func TestPlaybookOverlay_DefaultRequired(t *testing.T) {
	base := fstest.MapFS{}
	_, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
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
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestResolvePlaybook_MostSpecificWins — all four tiers present; a
// (claude-code, interactive) request resolves the tier-1
// <activity>.<runtime>.<mode>.md file.
func TestResolvePlaybook_MostSpecificWins(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":                         {Data: []byte("DEFAULT")},
		"plans/spec_refinement.claude-code.md":             {Data: []byte("PROVIDER")},
		"plans/spec_refinement.interactive.md":             {Data: []byte("MODE")},
		"plans/spec_refinement.claude-code.interactive.md": {Data: []byte("PROVIDER+MODE")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "PROVIDER+MODE", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.interactive.md", src)
}

// TestResolvePlaybook_ProviderBeatsModeAtEqualSpecificity — no tier-1
// file; tier 2 (provider overlay) outranks tier 3 (mode overlay).
func TestResolvePlaybook_ProviderBeatsModeAtEqualSpecificity(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":             {Data: []byte("DEFAULT")},
		"plans/spec_refinement.claude-code.md": {Data: []byte("PROVIDER")},
		"plans/spec_refinement.interactive.md": {Data: []byte("MODE")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "PROVIDER", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src)
}

// TestResolvePlaybook_FallsBackToDefault — only the default exists; a
// (claude-code, interactive) request falls all the way to tier 4.
func TestResolvePlaybook_FallsBackToDefault(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md": {Data: []byte("DEFAULT")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestResolvePlaybook_InteractiveOverlayResolvesOnlyForInteractiveMode —
// a tier-1 interactive overlay must NOT match a headless request, but
// MUST match an interactive request.
func TestResolvePlaybook_InteractiveOverlayResolvesOnlyForInteractiveMode(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":                         {Data: []byte("DEFAULT")},
		"plans/spec_refinement.claude-code.interactive.md": {Data: []byte("INTERACTIVE")},
	}

	// headless: tier 1 is skipped, no provider overlay → default.
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)

	// interactive: tier 1 matches.
	body, src, err = scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "INTERACTIVE", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.interactive.md", src)
}

// TestResolvePlaybook_HeadlessMatchesProviderOverlay — headless still
// resolves the provider overlay (tier 2), preserving the old two-tier
// behavior.
func TestResolvePlaybook_HeadlessMatchesProviderOverlay(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":             {Data: []byte("DEFAULT")},
		"plans/spec_refinement.claude-code.md": {Data: []byte("PROVIDER")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "PROVIDER", string(body))
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src)
}

// TestResolvePlaybook_EmptyRuntimeSkipsProviderTiers — empty runtime
// skips tiers 1 and 2; headless skips tier 3 → default.
func TestResolvePlaybook_EmptyRuntimeSkipsProviderTiers(t *testing.T) {
	base := fstest.MapFS{
		"plans/spec_refinement.md":             {Data: []byte("DEFAULT")},
		"plans/spec_refinement.claude-code.md": {Data: []byte("PROVIDER")},
	}
	body, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "DEFAULT", string(body))
	assert.Equal(t, "plans/spec_refinement.md", src)
}

// TestResolvePlaybook_MissingDefaultErrors — empty FS → error naming
// the canonical default path and "missing".
func TestResolvePlaybook_MissingDefaultErrors(t *testing.T) {
	base := fstest.MapFS{}
	_, src, err := scaffold.ResolvePlaybook(base, "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.Error(t, err)
	assert.Equal(t, "plans/spec_refinement.md", src)
	assert.Contains(t, err.Error(), "plans/spec_refinement.md")
	assert.Contains(t, strings.ToLower(err.Error()), "missing")
}
