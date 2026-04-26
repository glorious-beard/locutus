package dispatch

import (
	"context"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers ------------------------------------------------------------

// scriptedStreamingDriver is a multi-attempt StreamingDriver: each
// ParseStream call returns the next parser in a pre-scripted list, so a
// single driver can drive several runAttempt invocations with different
// event streams. Also records feedback passed to BuildRetryCommand so
// tests can assert the supervisor fed the right context into the retry.
type scriptedStreamingDriver struct {
	parsers   []*fakeStreamParser
	parserIdx int

	mu        sync.Mutex
	buildArgs []string // feedback values in order, one per attempt; "" for the initial attempt
}

func (d *scriptedStreamingDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	d.mu.Lock()
	d.buildArgs = append(d.buildArgs, "")
	d.mu.Unlock()
	return exec.CommandContext(ctx, "echo", "mock")
}

func (d *scriptedStreamingDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	d.mu.Lock()
	d.buildArgs = append(d.buildArgs, feedback)
	d.mu.Unlock()
	return exec.CommandContext(ctx, "echo", "mock-retry")
}

func (d *scriptedStreamingDriver) ParseStream(r io.Reader) StreamParser {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.parserIdx >= len(d.parsers) {
		// Scripted parsers exhausted — yield EOF immediately.
		return &fakeStreamParser{}
	}
	p := d.parsers[d.parserIdx]
	d.parserIdx++
	return p
}

func (d *scriptedStreamingDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "mock-resume"), nil
}

// feedbackAt returns the feedback passed to the Nth attempt (1-indexed).
// Attempt 1 is always the initial BuildCommand (empty feedback). Attempts
// ≥2 capture the feedback arg of BuildRetryCommand.
func (d *scriptedStreamingDriver) feedbackAt(attempt int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if attempt < 1 || attempt > len(d.buildArgs) {
		return ""
	}
	return d.buildArgs[attempt-1]
}

// cycleVerdictJSON is the mock FastLLM response that makes monitorCycle
// return IsCycle=true with high confidence.
const cycleVerdictJSON = `{"is_cycle":true,"confidence":0.9,"pattern":"file_thrashing","reasoning":"same file edited twice"}`

// cycleAgentDefs is the minimal AgentDefs that enables both the guardian
// (for permission events) and the cycle monitor.
func cycleAgentDefs() map[string]agent.AgentDef {
	return map[string]agent.AgentDef{
		"monitor":   {ID: "monitor"},
		"validator": {ID: "validator"},
	}
}

// --- Part 8 acceptance tests -------------------------------------------

func TestSupervise_ChurnOnceThenPass(t *testing.T) {
	// Attempt 1: 20 tool calls → monitor triggers → FastLLM flags cycle →
	// runAttempt returns *churnDetected.
	// Attempt 2: short happy-path stream → validator LLM says PASS.
	fast := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: cycleVerdictJSON},
	})
	validator := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: "PASS"},
	})

	sup := NewSupervisor(SupervisorConfig{
		LLM:        validator,
		FastLLM:    fast,
		MaxRetries: 3,
		AgentDefs:  cycleAgentDefs(),
	}, mockRunner())

	driver := &scriptedStreamingDriver{
		parsers: []*fakeStreamParser{
			{events: manyToolCalls(20)},
			{events: happyPathEvents("sess-2", "auth.go", "done")},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.True(t, outcome.Success, "second attempt should pass validation")
	assert.Equal(t, 2, outcome.Attempts)
	assert.Empty(t, outcome.Escalation, "single churn does not escalate")

	feedback := driver.feedbackAt(2)
	assert.Contains(t, feedback, "cycled",
		"retry feedback should surface that a cycle was detected")
	assert.Contains(t, feedback, "file_thrashing",
		"retry feedback should include the churn pattern for the agent")
}

func TestSupervise_TwoChurnsInWindowEscalates(t *testing.T) {
	// Both attempts churn → ≥2 of last 3 → escalate to RefineStep.
	fast := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: cycleVerdictJSON}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: cycleVerdictJSON}},
	)
	validator := agent.NewMockLLM() // should never be consulted

	sup := NewSupervisor(SupervisorConfig{
		LLM:        validator,
		FastLLM:    fast,
		MaxRetries: 3,
		AgentDefs:  cycleAgentDefs(),
	}, mockRunner())

	driver := &scriptedStreamingDriver{
		parsers: []*fakeStreamParser{
			{events: manyToolCalls(20)},
			{events: manyToolCalls(20)},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.False(t, outcome.Success)
	assert.Equal(t, string(EscalateRefineStep), outcome.Escalation,
		"two consecutive churns should escalate to RefineStep")
	assert.Equal(t, 2, outcome.Attempts)
	assert.Equal(t, 0, validator.CallCount(),
		"validator must not be invoked when both attempts churned")
}

func TestSupervise_AlternatingChurnFailChurn_Escalates(t *testing.T) {
	// churn → validation fail → churn.
	// Sliding window over last 3 attempts: [churn, fail, churn] = 2 churns.
	// This test is the regression guard for the sliding-window rule vs. the
	// simpler consecutive-churn counter it replaced.
	fast := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: cycleVerdictJSON}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: cycleVerdictJSON}},
	)
	validator := agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: "FAIL: intermediate failure"},
	})

	sup := NewSupervisor(SupervisorConfig{
		LLM:        validator,
		FastLLM:    fast,
		MaxRetries: 5,
		AgentDefs:  cycleAgentDefs(),
	}, mockRunner())

	driver := &scriptedStreamingDriver{
		parsers: []*fakeStreamParser{
			{events: manyToolCalls(20)},                                 // attempt 1: churn
			{events: happyPathEvents("sess-2", "auth.go", "attempt 2")}, // attempt 2: validation-fail
			{events: manyToolCalls(20)},                                 // attempt 3: churn
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.False(t, outcome.Success)
	assert.Equal(t, string(EscalateRefineStep), outcome.Escalation,
		"alternating churn/fail/churn must still escalate (sliding window: 2/3)")
	assert.Equal(t, 3, outcome.Attempts)
	assert.Equal(t, 1, validator.CallCount(),
		"validator should have been called exactly once (for the middle attempt)")
}

func TestSupervise_PermissionDuringAttempt_DoesNotSplitAttempt(t *testing.T) {
	// A permission interaction handled mid-attempt must not advance the
	// attempt counter. The attempt still runs to completion and gets
	// validated normally.
	bridge, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })

	// Guardian responds ALLOW to the permission prompt; validator responds
	// PASS to the acceptance check. Both go through s.cfg.LLM (same
	// "validator" agent).
	llm := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: "ALLOW"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: "PASS"}},
	)

	sup := NewSupervisor(SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
		AgentDefs:  map[string]agent.AgentDef{"validator": {ID: "validator"}},
	}, mockRunner())
	sup.permBridge = bridge

	// Scripted parser with 4 events; we'll pause mid-stream to let the
	// bridge event flow through, then unblock.
	parserCh := make(chan streamResult, 8)
	parser := &channelStreamParser{ch: parserCh}
	driver := &channelParserDriver{parser: parser}

	parserCh <- streamResult{evt: AgentEvent{Kind: EventInit, SessionID: "sess-perm"}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventText, Text: "thinking"}}

	// Run Supervise in a goroutine so the test goroutine can inject the
	// bridge event between parser events.
	type out struct {
		outcome *StepOutcome
		err     error
	}
	done := make(chan out, 1)
	go func() {
		outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
		done <- out{outcome, err}
	}()

	// Inject the permission event via the bridge socket, wait for the
	// bridge response so we know the supervisor handled it, then push
	// the terminal parser events.
	conn, err := net.Dial("unix", bridge.SocketPath)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	sendJSON(t, conn, PermRequest{ID: "req-1", Tool: "Bash", Input: map[string]any{"command": "ls"}})

	var bridgeResp PermDecision
	readJSON(t, conn, &bridgeResp)
	assert.Equal(t, "allow", bridgeResp.Behavior)

	// Permission was allowed; stream continues to completion.
	parserCh <- streamResult{evt: AgentEvent{Kind: EventResult, Text: "done"}}
	close(parserCh)

	result := <-done
	require.NoError(t, result.err)
	require.NotNil(t, result.outcome)
	assert.True(t, result.outcome.Success,
		"attempt that had a mid-stream permission event should still pass validation")
	assert.Equal(t, 1, result.outcome.Attempts,
		"permission interaction must NOT split the attempt")
	assert.Empty(t, result.outcome.Escalation)
	assert.Equal(t, 2, llm.CallCount(),
		"LLM should be called once for the guardian (ALLOW) and once for validation (PASS)")
}

func TestSupervise_ValidationFailNoChurn_Retries(t *testing.T) {
	// Validation fails on attempt 1, passes on attempt 2. No churn, no
	// escalation. Regression guard that validation failures alone don't
	// touch the churn sliding window.
	validator := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: "FAIL: missing impl"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: "PASS"}},
	)

	sup := NewSupervisor(SupervisorConfig{
		LLM:        validator,
		MaxRetries: 3,
		AgentDefs:  map[string]agent.AgentDef{"validator": {ID: "validator"}},
	}, mockRunner())

	driver := &scriptedStreamingDriver{
		parsers: []*fakeStreamParser{
			{events: happyPathEvents("sess-1", "auth.go", "attempt 1 output")},
			{events: happyPathEvents("sess-1", "auth.go", "attempt 2 output")},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.True(t, outcome.Success)
	assert.Equal(t, 2, outcome.Attempts)
	assert.Empty(t, outcome.Escalation)
	assert.Equal(t, 2, validator.CallCount())

	// Attempt 2's feedback should be the validator's FAIL verdict (not a
	// churn message, since no churn was detected).
	feedback := driver.feedbackAt(2)
	assert.Contains(t, feedback, "FAIL", "retry feedback should carry the validator's verdict")
	assert.NotContains(t, feedback, "cycled",
		"no churn was detected, so no cycle feedback should be injected")
}

// --- channelParserDriver -----------------------------------------------

// channelParserDriver is a single-parser StreamingDriver for tests that
// want to inject events into a channel-backed parser. Used for
// permission-during-attempt tests where precise timing matters.
type channelParserDriver struct {
	parser *channelStreamParser
}

func (d *channelParserDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock")
}
func (d *channelParserDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock-retry")
}
func (d *channelParserDriver) ParseStream(r io.Reader) StreamParser {
	return d.parser
}
func (d *channelParserDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "mock-resume"), nil
}

// countChurn is a small self-check of churnCountInLastN so a regression
// in the helper gets caught independently of Supervise behavior.
func TestChurnCountInLastN(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []outcomeKind
		n        int
		want     int
	}{
		{"empty", nil, 3, 0},
		{"single churn", []outcomeKind{outcomeChurn}, 3, 1},
		{"no churns", []outcomeKind{outcomeError, outcomeValidationFail}, 3, 0},
		{"two of three", []outcomeKind{outcomeChurn, outcomeValidationFail, outcomeChurn}, 3, 2},
		{"three of three", []outcomeKind{outcomeChurn, outcomeChurn, outcomeChurn}, 3, 3},
		{"old churn outside window", []outcomeKind{outcomeChurn, outcomeValidationFail, outcomeValidationFail, outcomeValidationFail}, 3, 0},
		{"window longer than slice", []outcomeKind{outcomeChurn}, 5, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, churnCountInLastN(c.outcomes, c.n))
		})
	}
}

// Silence unused-import warnings if this file's tests ever shrink.
var _ = strings.Contains
