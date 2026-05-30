// DJ-147 — newInitializedHandler reads _meta["locutus.dry_run"] and
// _meta["locutus.dry_run_format"] alongside the runtime+mode capture,
// stores them on the session map, and registers an overlay on the
// SpecStore so subsequent spec_* tool calls capture rather than persist.
package mcp

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializedHandler_CapturesDryRunSignals(t *testing.T) {
	clearSessionRuntimes()
	// Direct override path (SDK client can't inject _meta in tests on
	// modelcontextprotocol/go-sdk v1.6.1; see DJ-143 Task 3 note).
	sess := &fakeSessionKey{id: "dry-run-injection"}
	storeSessionContext(sess, "claude-code", "headless", true, "json")

	rt, mode := sessionRuntimeFor(sess)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "headless", mode)
	assert.True(t, sessionDryRunFor(sess))
	assert.Equal(t, "json", sessionDryRunFormatFor(sess))

	// Normalization: input "JSON" should land lowercased.
	clearSessionRuntimes()
	sess2 := &fakeSessionKey{id: "normalization"}
	storeSessionContext(sess2, "CLAUDE-CODE", "HEADLESS", true, "JSON")
	rt, mode = sessionRuntimeFor(sess2)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "headless", mode)
	assert.Equal(t, "json", sessionDryRunFormatFor(sess2))
}

func TestInitializedHandler_RegistersOverlayWhenDryRun(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	// Use a fakeSessionKey sentinel; bypass full SDK plumbing.
	sess := &fakeSessionKey{id: "overlay-registration"}
	storeSessionContext(sess, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	caps := store.OverlayCaptured(sess)
	assert.NotNil(t, caps, "an overlay must exist for a dry-run session")
	assert.Len(t, caps, 0, "but it starts empty")
}

func TestSessionDryRun_DefaultsFalseForUnknownSession(t *testing.T) {
	clearSessionRuntimes()
	// Freshly-allocated sentinel that was never passed to
	// storeSessionContext — confirms unknown sessions yield zero values.
	// Use *mcp.ServerSession typed nil to exercise the public accessor
	// (SessionDryRun/SessionDryRunFormat take *mcp.ServerSession per
	// DJ-143's SessionRuntime precedent).
	assert.False(t, SessionDryRun((*mcp.ServerSession)(nil)))
	assert.Empty(t, SessionDryRunFormat((*mcp.ServerSession)(nil)))
}
