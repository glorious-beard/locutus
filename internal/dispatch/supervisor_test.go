package dispatch

import (
	"context"
	"io"
	"os/exec"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// MockDriver is the streaming-era replacement for the old batch mock. It
// scripts a single stream of AgentEvents per ParseStream call; consumers
// that need multiple parsers across attempts should use
// scriptedStreamingDriver (supervise_test.go) instead.
type MockDriver struct {
	events []AgentEvent
}

// BuildCommand returns a no-op command (not actually executed in tests).
func (m *MockDriver) BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock")
}

// BuildRetryCommand returns a no-op retry command.
func (m *MockDriver) BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock-retry")
}

// ParseStream returns a parser that yields the scripted events once and
// then returns io.EOF forever. Note: MockDriver yields the same scripted
// stream on every ParseStream call, which is what the happy-path
// Supervise tests want for single-attempt cases.
func (m *MockDriver) ParseStream(r io.Reader) StreamParser {
	return &fakeStreamParser{events: m.events}
}

// RespondToAgent returns a no-op resume command.
func (m *MockDriver) RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "echo", "mock-resume"), nil
}

// mockRunner returns a CommandRunner that always succeeds with empty output.
// The bytes are ignored by MockDriver.ParseStream, but runAttempt's
// ReadCloser lifecycle still needs a closable stream.
func mockRunner() CommandRunner {
	return batchRunner([]byte(`{}`))
}

// mockLLMPass creates a MockLLM that always responds with "PASS".
func mockLLMPass(count int) *agent.MockExecutor {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}}
	}
	return agent.NewMockExecutor(responses...)
}

// mockLLMFailThenPass creates a MockLLM that fails N times then passes.
func mockLLMFailThenPass(failures int) *agent.MockExecutor {
	responses := make([]agent.MockResponse, failures+1)
	for i := 0; i < failures; i++ {
		responses[i] = agent.MockResponse{Response: &agent.AgentOutput{Content: "FAIL: missing error handling"}}
	}
	responses[failures] = agent.MockResponse{Response: &agent.AgentOutput{Content: "PASS"}}
	return agent.NewMockExecutor(responses...)
}

// mockLLMAlwaysFail creates a MockLLM that always responds with "FAIL".
func mockLLMAlwaysFail(count int) *agent.MockExecutor {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{Response: &agent.AgentOutput{Content: "FAIL: tests do not pass"}}
	}
	return agent.NewMockExecutor(responses...)
}

func newTestStep() spec.PlanStep {
	return spec.PlanStep{
		ID:          "step-1",
		Order:       1,
		ApproachID:  "strat-auth",
		Description: "Implement auth middleware",
		ExpectedFiles: []string{
			"internal/auth/middleware.go",
			"internal/auth/middleware_test.go",
		},
		Assertions: []spec.Assertion{
			{Kind: spec.AssertionKindTestPass, Target: "./internal/auth/..."},
			{Kind: spec.AssertionKindCompiles},
		},
	}
}

// happyPathEvents returns a compact event sequence representing a clean
// claude run: init, tool call, tool result, final result. Few enough
// events that the monitor never triggers (default checkEveryEvents=15).
func happyPathEvents(sessionID, file, finalText string) []AgentEvent {
	return []AgentEvent{
		{Kind: EventInit, SessionID: sessionID},
		{Kind: EventToolCall, ToolName: "Write", ToolInput: map[string]any{"file_path": file}, FilePaths: []string{file}},
		{Kind: EventToolResult, Text: "ok"},
		{Kind: EventResult, Text: finalText, SessionID: sessionID},
	}
}

func TestSupervisePassesFirstAttempt(t *testing.T) {
	llm := mockLLMPass(1)
	cfg := SupervisorConfig{LLM: llm, MaxRetries: 3}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{events: happyPathEvents("sess-1", "internal/auth/middleware.go", "done")}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.True(t, outcome.Success, "should pass on first attempt")
	assert.Equal(t, 1, outcome.Attempts)
	assert.Empty(t, outcome.Escalation, "no escalation on success")
	assert.Contains(t, outcome.Files, "internal/auth/middleware.go")
}

func TestSuperviseRetriesOnFailure(t *testing.T) {
	// First validation fails, second passes. Both attempts stream a clean
	// happy-path sequence; the retry is driven by validator verdict.
	llm := mockLLMFailThenPass(1)
	cfg := SupervisorConfig{LLM: llm, MaxRetries: 3}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{events: happyPathEvents("sess-1", "auth.go", "attempt output")}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.True(t, outcome.Success, "should succeed after retry")
	assert.Equal(t, 2, outcome.Attempts)
	assert.Empty(t, outcome.Escalation)
}

func TestSuperviseExhaustsRetries(t *testing.T) {
	// All 3 attempts validate as FAIL → no success, no escalation (no churn
	// detected; just repeated validation failures).
	llm := mockLLMAlwaysFail(3)
	cfg := SupervisorConfig{LLM: llm, MaxRetries: 3}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{events: happyPathEvents("sess-1", "auth.go", "output")}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.False(t, outcome.Success, "should fail after exhausting retries")
	assert.Equal(t, 3, outcome.Attempts)
	assert.Empty(t, outcome.Escalation,
		"validation-only failures do not escalate; only sliding-window churn does")
}
