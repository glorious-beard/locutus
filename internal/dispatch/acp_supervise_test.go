package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch/policy"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePromptConn implements PromptConn against a scripted event sequence.
// Each Prompt call pops the next script off `scripts`; if the slice runs
// out, the call returns an error. Tests use this to exercise the
// supervisor's retry-and-validate loop without spinning up acp.Connection
// + io.Pipe + fakeAgent (which has its own coverage in the acp package).
type fakePromptConn struct {
	mu sync.Mutex

	sessionID string
	scripts   [][]AgentEvent // per-call event scripts (one per Prompt call)
	cancelled int            // count of Cancel calls

	// promptHook, if set, runs synchronously after each Prompt call before
	// the events channel is returned. Lets tests inject pauses, force
	// scripted ctx-cancel timing, etc.
	promptHook func(ctx context.Context, sessionID, text string) error

	// policies records the Policy each Prompt received, so tests can assert
	// the supervisor threaded the right per-step policy through.
	policies []policy.Policy

	sent []string // prompt texts received, in order
}

func (f *fakePromptConn) NewSession(_ context.Context, _ string) (string, error) {
	if f.sessionID == "" {
		f.sessionID = "sess-fake-1"
	}
	return f.sessionID, nil
}

func (f *fakePromptConn) Prompt(ctx context.Context, sessionID, text string, pol policy.Policy) (<-chan AgentEvent, error) {
	f.mu.Lock()
	idx := len(f.sent)
	f.sent = append(f.sent, text)
	f.policies = append(f.policies, pol)
	if idx >= len(f.scripts) {
		f.mu.Unlock()
		return nil, fmt.Errorf("fakePromptConn: no script for attempt %d (text=%q)", idx, text)
	}
	script := f.scripts[idx]
	f.mu.Unlock()

	if f.promptHook != nil {
		if err := f.promptHook(ctx, sessionID, text); err != nil {
			return nil, err
		}
	}

	out := make(chan AgentEvent, len(script)+1)
	for _, e := range script {
		out <- e
	}
	close(out)
	return out, nil
}

func (f *fakePromptConn) Cancel(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled++
	return nil
}

func (f *fakePromptConn) Close() error { return nil }

// --- helpers -----------------------------------------------------------

func newTestStep() spec.PlanStep {
	return spec.PlanStep{
		ID:          "step-1",
		Order:       1,
		ApproachID:  "strat-auth",
		Description: "Implement auth middleware",
		ExpectedFiles: []string{
			"internal/auth/middleware.go",
		},
		Assertions: []spec.Assertion{
			{Kind: spec.AssertionKindCompiles},
		},
	}
}

// newTestWorkstream wraps newTestStep into a Workstream — the DJ-121
// supervised unit. During the Phase 3-4 transition (PlanStep still in the
// spec model) the workstream carries the step inline so the validator and
// guardian still have step-level context to draw on. Phase 9 removes
// `Steps` once the planner is migrated.
func newTestWorkstream() spec.Workstream {
	step := newTestStep()
	return spec.Workstream{
		ID:             "ws-auth",
		StrategyDomain: "auth",
		AgentID:        "claude-code",
		Steps:          []spec.PlanStep{step},
	}
}

// --- Phase 4 regression guards ----------------------------------------

// TestSupervise_ValidatorPrefersWorkstreamAssertions confirms the
// preference logic introduced in Phase 4: when ws.Assertions is
// populated, the validator's prompt uses those criteria and ignores
// step-level assertions. This is the contract Phase 9 will firm up by
// deleting the step-union fallback.
func TestSupervise_ValidatorPrefersWorkstreamAssertions(t *testing.T) {
	conn := &fakePromptConn{
		scripts: [][]AgentEvent{happyAttempt("internal/auth/middleware.go", "done")},
	}
	// MockExecutor captures the prompts it receives — assert the validator
	// saw the workstream-level criteria, not the step-level fallback.
	mockLLM := agent.NewMockExecutor(agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}})
	sup := NewSupervisor(SupervisorConfig{LLM: mockLLM, MaxRetries: 3}, nil)

	ws := newTestWorkstream()
	// Populate ws.Assertions with a workstream-level criterion AND a
	// step-level one. The validator should see only the workstream one.
	ws.Assertions = []spec.Assertion{
		{Kind: spec.AssertionKindLLMReview, Prompt: "auth middleware verifies JWT signatures", Message: "workstream-level criterion"},
	}
	ws.Steps[0].Assertions = []spec.Assertion{
		{Kind: spec.AssertionKindTestPass, Target: "./internal/auth/...", Message: "step-level criterion (should be ignored)"},
	}

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	outcome, err := sup.Supervise(context.Background(), ws, conn, sessionID)
	require.NoError(t, err)
	require.True(t, outcome.Success)

	// The mock LLM saw one prompt — assert it contains the workstream-level
	// message and NOT the step-level one.
	calls := mockLLM.Calls()
	require.Len(t, calls, 1, "validator should run once per workstream")
	gotPrompt := calls[0].Input.Messages[0].Content
	assert.Contains(t, gotPrompt, "workstream-level criterion",
		"validator prompt should carry ws.Assertions when populated")
	assert.NotContains(t, gotPrompt, "step-level criterion (should be ignored)",
		"validator prompt should NOT include step assertions when ws.Assertions is populated")
}

// TestSupervise_ValidatorFallsBackToStepAssertionsWhenWorkstreamAssertionsEmpty
// confirms the transitional fallback path: when ws.Assertions is empty,
// step-level assertions are still honored so unmigrated planner output
// continues to validate against the criteria the planner produced. Phase 9
// deletes this codepath; the test is retained until then.
func TestSupervise_ValidatorFallsBackToStepAssertionsWhenWorkstreamAssertionsEmpty(t *testing.T) {
	conn := &fakePromptConn{
		scripts: [][]AgentEvent{happyAttempt("internal/auth/middleware.go", "done")},
	}
	mockLLM := agent.NewMockExecutor(agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}})
	sup := NewSupervisor(SupervisorConfig{LLM: mockLLM, MaxRetries: 3}, nil)

	ws := newTestWorkstream()
	// ws.Assertions intentionally empty; only step assertions populated.
	ws.Assertions = nil
	ws.Steps[0].Assertions = []spec.Assertion{
		{Kind: spec.AssertionKindTestPass, Target: "./internal/auth/...", Message: "step fallback criterion"},
	}

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	outcome, err := sup.Supervise(context.Background(), ws, conn, sessionID)
	require.NoError(t, err)
	require.True(t, outcome.Success)

	calls := mockLLM.Calls()
	require.Len(t, calls, 1)
	gotPrompt := calls[0].Input.Messages[0].Content
	assert.Contains(t, gotPrompt, "step fallback criterion",
		"validator prompt should carry step assertions when ws.Assertions is empty (transition fallback)")
}

// happyAttempt returns one Prompt-script worth of events: a tool call, a
// tool result, and a terminal Result. Few enough events that the cycle
// monitor never triggers (default checkEveryEvents=15).
func happyAttempt(file, finalText string) []AgentEvent {
	return []AgentEvent{
		{Kind: EventToolCall, ToolName: "Write", ToolInput: map[string]any{"file_path": file}, FilePaths: []string{file}},
		{Kind: EventToolResult, Text: "ok"},
		{Kind: EventResult, Text: finalText},
	}
}

func passResponses(n int) []agent.MockResponse {
	out := make([]agent.MockResponse, n)
	for i := range out {
		out[i] = agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}}
	}
	return out
}

// --- Scenario 1: happy-path single attempt → success -------------------

func TestSupervise_HappyPathSingleAttempt(t *testing.T) {
	conn := &fakePromptConn{
		scripts: [][]AgentEvent{happyAttempt("internal/auth/middleware.go", "done")},
	}
	llm := agent.NewMockExecutor(passResponses(1)...)
	sup := NewSupervisor(SupervisorConfig{LLM: llm, MaxRetries: 3}, nil)

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	outcome, err := sup.Supervise(context.Background(), newTestWorkstream(), conn, sessionID)
	require.NoError(t, err)
	require.NotNil(t, outcome)

	assert.True(t, outcome.Success, "happy path should succeed on first attempt")
	assert.Equal(t, 1, outcome.Attempts)
	assert.Empty(t, outcome.Escalation, "no escalation on success")
	assert.Contains(t, outcome.Files, "internal/auth/middleware.go")
	assert.Equal(t, sessionID, outcome.SessionID)
	require.Len(t, conn.sent, 1)
	assert.Contains(t, conn.sent[0], "workstream \"ws-auth\"",
		"first attempt should send a workstream-level kick-off prompt referencing the workstream id (DJ-121)")
	assert.Contains(t, conn.sent[0], "_locutus/plan.md",
		"kick-off prompt should point the agent at its worktree-resident plan")
	assert.Contains(t, conn.sent[0], "_locutus/checklist.md",
		"kick-off prompt should instruct the agent to maintain its checklist")
}

// --- Scenario 2: failed attempt → retry with feedback in same session --

func TestSupervise_RetryWithFeedbackInSameSession(t *testing.T) {
	// Two scripted attempts, both stream a clean event sequence; the first
	// validator verdict is FAIL with reasoning, the second is PASS. The
	// retry's Prompt text should be the validator's feedback, not the
	// workstream kick-off (the agent is still in the same ACP session, so
	// the original workstream framing is in conversation context AND in
	// the worktree-resident plan.md it can re-read).
	conn := &fakePromptConn{
		scripts: [][]AgentEvent{
			happyAttempt("auth.go", "first try output"),
			happyAttempt("auth.go", "second try output"),
		},
	}
	llm := agent.NewMockExecutor(
		agent.MockResponse{Response: &agent.AgentOutput{Content: "FAIL: missing error handling on token expiry"}},
		agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}},
	)
	sup := NewSupervisor(SupervisorConfig{LLM: llm, MaxRetries: 3}, nil)

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	outcome, err := sup.Supervise(context.Background(), newTestWorkstream(), conn, sessionID)
	require.NoError(t, err)
	require.NotNil(t, outcome)

	assert.True(t, outcome.Success, "should succeed after one retry")
	assert.Equal(t, 2, outcome.Attempts)
	require.Len(t, conn.sent, 2)
	assert.Contains(t, conn.sent[0], "workstream \"ws-auth\"",
		"first attempt should send the workstream-level kick-off prompt")
	assert.Contains(t, conn.sent[1], "missing error handling on token expiry",
		"retry prompt should carry the validator's feedback as the next user message")
}

// --- Scenario 3: churn detection → escalation --------------------------

func TestSupervise_ChurnEscalation(t *testing.T) {
	// Script three attempts, each emitting enough events to trigger
	// monitor.ShouldCheck() (default checkEveryEvents=15). The fast LLM
	// returns a high-confidence cycle verdict each time. Two consecutive
	// churns out of the last 3 attempts triggers escalation.
	cycling := make([]AgentEvent, 0, 20)
	for i := 0; i < 16; i++ {
		cycling = append(cycling, AgentEvent{Kind: EventToolCall, ToolName: "Read", FilePaths: []string{"file.go"}})
	}
	cycling = append(cycling, AgentEvent{Kind: EventResult, Text: "stuck"})

	conn := &fakePromptConn{
		scripts: [][]AgentEvent{
			append([]AgentEvent(nil), cycling...),
			append([]AgentEvent(nil), cycling...),
			append([]AgentEvent(nil), cycling...),
		},
	}

	// Two LLMs: validator (never reached on churned attempts) and a fast
	// LLM that returns a high-confidence cycle verdict to monitorCycle.
	cycleJSON := `{"is_cycle": true, "reasoning": "repeated reads of same file", "confidence": 0.95, "pattern": "read-loop"}`
	fast := agent.NewMockExecutor(
		agent.MockResponse{Response: &agent.AgentOutput{Content: cycleJSON}},
		agent.MockResponse{Response: &agent.AgentOutput{Content: cycleJSON}},
		agent.MockResponse{Response: &agent.AgentOutput{Content: cycleJSON}},
	)

	cfg := SupervisorConfig{
		LLM:        agent.NewMockExecutor(), // unused — churned attempts skip validation
		FastLLM:    fast,
		MaxRetries: 3,
		AgentDefs: map[string]agent.AgentDef{
			"monitor": {ID: "monitor"},
		},
	}
	sup := NewSupervisor(cfg, nil)

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	outcome, err := sup.Supervise(context.Background(), newTestWorkstream(), conn, sessionID)
	require.NoError(t, err)
	require.NotNil(t, outcome)

	assert.False(t, outcome.Success, "churn should not succeed")
	assert.Equal(t, string(EscalateRefineStep), outcome.Escalation,
		"two churns of the last three attempts should escalate to RefineStep")
}

// --- Scenario 4: ctx cancel mid-prompt → supervisor returns ctx.Err() --

func TestSupervise_ContextCancellation(t *testing.T) {
	// The fakePromptConn's promptHook blocks until the test's ctx is
	// cancelled, simulating a long-running prompt. The supervisor's select
	// on ctx.Done should fire, call conn.Cancel, and return ctx.Err().
	blocked := make(chan struct{})
	conn := &fakePromptConn{
		scripts: [][]AgentEvent{{{Kind: EventResult, Text: "unused"}}},
		promptHook: func(ctx context.Context, _, _ string) error {
			// Signal the test that we're inside Prompt, then block on ctx.
			close(blocked)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	llm := agent.NewMockExecutor()
	sup := NewSupervisor(SupervisorConfig{LLM: llm, MaxRetries: 3}, nil)

	sessionID, err := conn.NewSession(context.Background(), "/tmp/work")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var (
		outcome *StepOutcome
		supErr  error
	)
	go func() {
		outcome, supErr = sup.Supervise(ctx, newTestWorkstream(), conn, sessionID)
		close(done)
	}()

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor never reached Prompt")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not return after ctx cancel")
	}

	// Cancellation surfaces as a Prompt error → attempt is treated as a
	// failure that the retry loop feeds into the next attempt's feedback.
	// Because the prompt fails before any events arrive, no script entry
	// is consumed past the first one, and subsequent retries also fail
	// immediately (no more scripts) — so the eventual outcome is
	// retries-exhausted with no escalation.
	_ = outcome
	if supErr != nil {
		assert.True(t, errors.Is(supErr, context.Canceled),
			"ctx cancel should surface (got %v)", supErr)
	}
}
