package dispatch

import (
	"context"
	"os/exec"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// MockDriver provides scripted outputs for testing the supervisor loop.
type MockDriver struct {
	outputs []DriverOutput
	pos     int
}

// BuildCommand returns a no-op command (not actually executed in tests).
func (m *MockDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.Command("echo", "mock")
}

// BuildRetryCommand returns a no-op retry command.
func (m *MockDriver) BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd {
	return exec.Command("echo", "mock retry")
}

// ParseOutput returns the next scripted DriverOutput.
func (m *MockDriver) ParseOutput(out []byte) (DriverOutput, error) {
	if m.pos >= len(m.outputs) {
		return DriverOutput{}, nil
	}
	o := m.outputs[m.pos]
	m.pos++
	return o, nil
}

// mockRunner returns a CommandRunner that always succeeds with empty output.
func mockRunner() CommandRunner {
	return func(cmd *exec.Cmd) ([]byte, error) {
		return []byte(`{}`), nil
	}
}

// mockLLMPass creates a MockLLM that always responds with "PASS".
func mockLLMPass(count int) *agent.MockLLM {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{
			Response: &agent.GenerateResponse{
				Content: "PASS",
			},
		}
	}
	return agent.NewMockLLM(responses...)
}

// mockLLMFailThenPass creates a MockLLM that fails N times then passes.
func mockLLMFailThenPass(failures int) *agent.MockLLM {
	responses := make([]agent.MockResponse, failures+1)
	for i := 0; i < failures; i++ {
		responses[i] = agent.MockResponse{
			Response: &agent.GenerateResponse{
				Content: "FAIL: missing error handling",
			},
		}
	}
	responses[failures] = agent.MockResponse{
		Response: &agent.GenerateResponse{
			Content: "PASS",
		},
	}
	return agent.NewMockLLM(responses...)
}

// mockLLMAlwaysFail creates a MockLLM that always responds with "FAIL".
func mockLLMAlwaysFail(count int) *agent.MockLLM {
	responses := make([]agent.MockResponse, count)
	for i := range responses {
		responses[i] = agent.MockResponse{
			Response: &agent.GenerateResponse{
				Content: "FAIL: tests do not pass",
			},
		}
	}
	return agent.NewMockLLM(responses...)
}

func newTestStep() spec.PlanStep {
	return spec.PlanStep{
		ID:          "step-1",
		Order:       1,
		StrategyID:  "strat-auth",
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

func TestSupervisePassesFirstAttempt(t *testing.T) {
	llm := mockLLMPass(1)
	cfg := SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
	}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{
		outputs: []DriverOutput{
			{Success: true, Files: []string{"internal/auth/middleware.go"}, SessionID: "sess-1", Output: "done"},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.True(t, outcome.Success, "should pass on first attempt")
	assert.Equal(t, 1, outcome.Attempts)
	assert.Empty(t, outcome.Escalation, "no escalation on success")
}

func TestSuperviseRetriesOnFailure(t *testing.T) {
	// First validation fails, second passes.
	llm := mockLLMFailThenPass(1)
	cfg := SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
	}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{
		outputs: []DriverOutput{
			{Success: true, Files: []string{"auth.go"}, SessionID: "sess-1", Output: "attempt 1"},
			{Success: true, Files: []string{"auth.go"}, SessionID: "sess-1", Output: "attempt 2"},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.True(t, outcome.Success, "should succeed after retry")
	assert.Equal(t, 2, outcome.Attempts)
	assert.Empty(t, outcome.Escalation)
}

func TestSuperviseExhaustsRetries(t *testing.T) {
	// All 3 attempts fail validation.
	llm := mockLLMAlwaysFail(3)
	cfg := SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
	}
	sup := NewSupervisor(cfg, mockRunner())

	driver := &MockDriver{
		outputs: []DriverOutput{
			{Success: true, Files: []string{"auth.go"}, SessionID: "sess-1", Output: "attempt 1"},
			{Success: true, Files: []string{"auth.go"}, SessionID: "sess-1", Output: "attempt 2"},
			{Success: true, Files: []string{"auth.go"}, SessionID: "sess-1", Output: "attempt 3"},
		},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.False(t, outcome.Success, "should fail after exhausting retries")
	assert.Equal(t, 3, outcome.Attempts)
}

func TestSuperviseDetectsStuck(t *testing.T) {
	// Two identical failure outputs should trigger stuck detection.
	llm := mockLLMAlwaysFail(3)
	cfg := SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
	}
	sup := NewSupervisor(cfg, mockRunner())

	// Return identical output on every attempt to trigger stuck detection.
	identicalOutput := DriverOutput{
		Success:   true,
		Files:     []string{"auth.go"},
		SessionID: "sess-1",
		Output:    "identical output each time",
	}
	driver := &MockDriver{
		outputs: []DriverOutput{identicalOutput, identicalOutput, identicalOutput},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.False(t, outcome.Success, "stuck loop should not be marked as success")
	assert.NotEmpty(t, outcome.Escalation, "escalation should be triggered when stuck")
}

func TestSuperviseEscalationCascade(t *testing.T) {
	// After stuck detection, the first escalation level should be "refine_step".
	llm := mockLLMAlwaysFail(3)
	cfg := SupervisorConfig{
		LLM:        llm,
		MaxRetries: 3,
	}
	sup := NewSupervisor(cfg, mockRunner())

	identicalOutput := DriverOutput{
		Success:   true,
		Files:     []string{"auth.go"},
		SessionID: "sess-1",
		Output:    "same output verbatim",
	}
	driver := &MockDriver{
		outputs: []DriverOutput{identicalOutput, identicalOutput, identicalOutput},
	}

	outcome, err := sup.Supervise(context.Background(), newTestStep(), driver, "/tmp/work")
	assert.NoError(t, err)
	assert.NotNil(t, outcome)
	assert.Equal(t, string(EscalateRefineStep), outcome.Escalation,
		"first escalation level should be refine_step")
}
