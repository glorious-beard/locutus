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
