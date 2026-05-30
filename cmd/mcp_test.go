// DJ-143 — McpCmd reads LOCUTUS_MODE env at startup, defaulting to
// "interactive" when unset. The resolved mode flows to
// BridgeStdioToSocket which forwards it to the daemon via
// _meta["locutus.mode"].
package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveLocutusModeUnsetDefaultsToInteractive(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "")
	assert.Equal(t, "interactive", resolveLocutusMode())
}

func TestResolveLocutusModeHeadless(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "headless")
	assert.Equal(t, "headless", resolveLocutusMode())
}

func TestResolveLocutusModeNormalizes(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "  Headless  ")
	assert.Equal(t, "headless", resolveLocutusMode())
}

// DJ-147 — McpCmd reads LOCUTUS_DRY_RUN + LOCUTUS_DRY_RUN_FORMAT env at
// startup and forwards them via _meta on the initialize request so the
// daemon's session-context module can flip the session into dry-run
// mode. Mirrors the LOCUTUS_MODE pattern above.

func TestResolveLocutusDryRun_UnsetIsFalse(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN", "")
	assert.False(t, resolveLocutusDryRun())
}

func TestResolveLocutusDryRun_TruthyVariants(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "  True  "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("LOCUTUS_DRY_RUN", v)
			assert.True(t, resolveLocutusDryRun())
		})
	}
}

func TestResolveLocutusDryRun_FalsyVariants(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "anything-else"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("LOCUTUS_DRY_RUN", v)
			assert.False(t, resolveLocutusDryRun())
		})
	}
}

func TestResolveLocutusDryRunFormat_DefaultMarkdown(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat())
}

func TestResolveLocutusDryRunFormat_AcceptsJSONAndMarkdown(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "json")
	assert.Equal(t, "json", resolveLocutusDryRunFormat())
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "MARKDOWN")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat())
}

func TestResolveLocutusDryRunFormat_UnknownFallsBack(t *testing.T) {
	t.Setenv("LOCUTUS_DRY_RUN_FORMAT", "xml")
	assert.Equal(t, "markdown", resolveLocutusDryRunFormat(), "unknown values fall back to the safe default")
}
