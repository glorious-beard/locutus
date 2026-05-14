package acp_test

// Opt-in end-to-end smoke test for the ACP transport against a real
// ACP-server subprocess. Replaces the pre-DJ-119 NDJSON-streaming
// live test (`internal/dispatch/live_integration_test.go`, deleted in
// Phase 3).
//
// Gated by the LOCUTUS_LIVE_ACP env var so it never runs during
// `go test ./...` on CI or a fresh dev box. Pick which agent to drive
// by setting LOCUTUS_LIVE_ACP to one of the registered agent ids:
//
//	LOCUTUS_LIVE_ACP=gemini       go test -run TestLiveACP ./internal/dispatch/acp/
//	LOCUTUS_LIVE_ACP=claude-code  go test -run TestLiveACP ./internal/dispatch/acp/
//	LOCUTUS_LIVE_ACP=codex        go test -run TestLiveACP ./internal/dispatch/acp/
//
// Each agent's binary must already be on $PATH (see `locutus init`'s
// preflight check, Phase 7). The test:
//
//   - Spawns the real subprocess via acp.Open
//   - Verifies the initialize handshake reports a capability set
//   - Creates a session rooted at a temp dir
//   - Sends one trivial prompt ("Reply with just the word OK.")
//   - Drains the event stream and asserts a terminal EventResult
//   - Closes the connection (subprocess teardown)
//
// This is a smoke test, not a behavior test — verifies the wire end-to-end,
// not what the model says. Pick prompts the model is unlikely to ask
// permission for (no tool use), so this run also covers no-policy paths.
//
// Known teardown caveat: acp.Connection wires the subprocess's stderr to
// os.Stderr (so production users see diagnostics). On some agents that
// continue writing to stderr after we send Kill, this can hold the test
// binary's stderr FD open past `go test`'s WaitDelay. The test assertions
// will already have passed; the resulting "exec: WaitDelay expired" is a
// cleanup artifact, not a regression signal. Look for the t.Logf lines
// before the warning to verify the actual outcome.

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/dispatch/acp"
	"github.com/stretchr/testify/require"
)

const liveEnvVar = "LOCUTUS_LIVE_ACP"

func TestLiveACP(t *testing.T) {
	agentID := os.Getenv(liveEnvVar)
	if agentID == "" {
		t.Skipf("set %s=<agent-id> to run; supported ids: claude-code, codex, gemini", liveEnvVar)
	}
	spawn, ok := acp.AgentSpawns[agentID]
	if !ok {
		t.Fatalf("%s=%q: unknown agent id (known: claude-code, codex, gemini)", liveEnvVar, agentID)
	}
	if _, err := exec.LookPath(spawn.Cmd); err != nil {
		t.Skipf("%s=%q but %s not on $PATH: %v (run `locutus init` for install hints)",
			liveEnvVar, agentID, spawn.Cmd, err)
	}

	// Generous timeout — real LLMs can take seconds to respond. Capped so a
	// silently-hung subprocess doesn't deadlock the test binary.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := acp.Open(ctx, spawn, "")
	require.NoError(t, err, "acp.Open should succeed for %s", agentID)
	defer func() { _ = conn.Close() }()

	caps := conn.Capabilities()
	t.Logf("%s capabilities: loadSession=%v promptCapabilities=%+v",
		agentID, caps.LoadSession, caps.PromptCapabilities)

	cwd, err := os.MkdirTemp("", "locutus-live-acp-*")
	require.NoError(t, err)
	defer os.RemoveAll(cwd)

	sessionID, err := conn.NewSession(ctx, cwd)
	require.NoError(t, err, "NewSession should succeed")
	require.NotEmpty(t, sessionID, "session id should be non-empty")
	t.Logf("session: %s", sessionID)

	events, err := conn.Prompt(ctx, sessionID, "Reply with just the word OK.", nil)
	require.NoError(t, err, "Prompt should succeed")

	var (
		textCount   int
		toolCount   int
		terminal    dispatch.AgentEvent
		seenAny     bool
		eventTotals = make(map[dispatch.EventKind]int)
	)
	for ev := range events {
		seenAny = true
		eventTotals[ev.Kind]++
		switch ev.Kind {
		case dispatch.EventText:
			textCount++
		case dispatch.EventToolCall, dispatch.EventToolResult:
			toolCount++
		}
		terminal = ev
	}
	t.Logf("event totals: %+v", eventTotals)

	require.True(t, seenAny, "expected at least one event from the agent")
	require.Contains(t, []dispatch.EventKind{dispatch.EventResult, dispatch.EventError}, terminal.Kind,
		"terminal event must be Result or Error")

	if terminal.Kind == dispatch.EventError {
		t.Logf("agent surfaced an error: %s", terminal.Text)
		t.Fatalf("live agent returned EventError; investigate before treating this test as a regression signal")
	}
	t.Logf("terminal stopReason: %q (text=%d, tool=%d)", terminal.Text, textCount, toolCount)
}
