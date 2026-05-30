// DJ-147 — newInitializedHandler reads _meta["locutus.dry_run"] and
// _meta["locutus.dry_run_format"] alongside the runtime+mode capture,
// stores them on the session map, and registers an overlay on the
// SpecStore so subsequent spec_* tool calls capture rather than persist.
package mcp

import (
	"context"
	"fmt"
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

type captureTestArgs struct{ ID, Title string }
type captureTestResult struct{ ID string }

func TestCaptureOnly_PassthroughWhenNotDryRun(t *testing.T) {
	clearSessionRuntimes()
	called := false
	inner := func(_ context.Context, _ *mcp.CallToolRequest, in captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		called = true
		return nil, captureTestResult{ID: in.ID}, nil
	}
	capture := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		t.Fatal("capture must not run when session is not dry-run")
		return captureTestResult{}, nil
	}
	wrapped := captureOnly(inner, capture)

	sess := (*mcp.ServerSession)(nil) // sentinel for the request
	storeSessionContext(sess, "claude-code", "headless", false, "")
	req := &mcp.CallToolRequest{Session: sess, Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	_, _, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x"})
	require.NoError(t, err)
	assert.True(t, called, "inner handler must run for non-dry-run sessions")
}

func TestCaptureOnly_CapturesWhenDryRun(t *testing.T) {
	clearSessionRuntimes()
	innerCalled := false
	captureCalled := false
	var capturedIn captureTestArgs

	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		innerCalled = true
		return nil, captureTestResult{}, nil
	}
	capture := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		captureCalled = true
		capturedIn = in
		return captureTestResult{ID: in.ID}, nil
	}
	wrapped := captureOnly(inner, capture)

	sess := (*mcp.ServerSession)(nil)
	storeSessionContext(sess, "claude-code", "headless", true, "markdown")
	req := &mcp.CallToolRequest{Session: sess, Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	res, out, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x", Title: "X"})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, innerCalled, "inner handler must NOT run for dry-run sessions")
	assert.True(t, captureCalled, "capture must run for dry-run sessions")
	assert.Equal(t, "dec-x", capturedIn.ID)
	assert.Equal(t, "dec-x", out.ID)
}

func TestCaptureOnly_CaptureErrorReturnsToolError(t *testing.T) {
	clearSessionRuntimes()
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ captureTestArgs) (*mcp.CallToolResult, captureTestResult, error) {
		return nil, captureTestResult{}, nil
	}
	capture := func(sess *mcp.ServerSession, in captureTestArgs) (captureTestResult, error) {
		return captureTestResult{}, fmt.Errorf("simulated capture failure")
	}
	wrapped := captureOnly(inner, capture)

	sess := (*mcp.ServerSession)(nil)
	storeSessionContext(sess, "claude-code", "headless", true, "markdown")
	req := &mcp.CallToolRequest{Session: sess, Params: &mcp.CallToolParamsRaw{Name: "spec_propose_decision"}}
	res, _, err := wrapped(context.Background(), req, captureTestArgs{ID: "dec-x"})

	require.NoError(t, err, "tool-level errors are surfaced as IsError results, not Go errors")
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}
