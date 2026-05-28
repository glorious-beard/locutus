// DJ-143 — the headless ACP-dispatch path sets LOCUTUS_MODE=headless on
// the spawned coding-agent's env so its child `locutus mcp` bridge can
// forward the mode to the daemon via _meta["locutus.mode"].
package runner

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/stretchr/testify/assert"
)

func TestHeadlessSpawnEnvIncludesLocutusMode(t *testing.T) {
	base := acp.Spawn{Cmd: "claude-agent-acp"}
	got := headlessSpawnEnv(base, []string{"PATH=/usr/bin", "HOME=/home/x"})

	// Original env passes through.
	assert.Contains(t, got.Env, "PATH=/usr/bin")
	assert.Contains(t, got.Env, "HOME=/home/x")
	// LOCUTUS_MODE=headless is appended.
	assert.Contains(t, got.Env, "LOCUTUS_MODE=headless")
	// Cmd is unchanged.
	assert.Equal(t, "claude-agent-acp", got.Cmd)
}

func TestHeadlessSpawnEnvOverridesPreExistingLocutusMode(t *testing.T) {
	base := acp.Spawn{Cmd: "claude-agent-acp"}
	// An inherited LOCUTUS_MODE=interactive would mislead the bridge;
	// the headless path must take precedence.
	got := headlessSpawnEnv(base, []string{"LOCUTUS_MODE=interactive", "PATH=/usr/bin"})

	// PATH still there.
	assert.Contains(t, got.Env, "PATH=/usr/bin")
	// Exactly one LOCUTUS_MODE entry, with value=headless.
	headlessCount := 0
	for _, e := range got.Env {
		if strings.HasPrefix(e, "LOCUTUS_MODE=") {
			assert.Equal(t, "LOCUTUS_MODE=headless", e)
			headlessCount++
		}
	}
	assert.Equal(t, 1, headlessCount, "exactly one LOCUTUS_MODE entry, value=headless")
}

func TestHeadlessSpawnEnvPreservesBaseFields(t *testing.T) {
	base := acp.Spawn{Cmd: "gemini", Args: []string{"--acp"}}
	got := headlessSpawnEnv(base, []string{"PATH=/usr/bin"})
	assert.Equal(t, "gemini", got.Cmd)
	assert.Equal(t, []string{"--acp"}, got.Args)
}
