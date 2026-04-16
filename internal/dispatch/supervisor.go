package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// DriverOutput holds the result of running a coding agent.
type DriverOutput struct {
	Success   bool
	Files     []string
	SessionID string
	Output    string
}

// AgentDriver builds commands for a coding agent.
type AgentDriver interface {
	BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd
	BuildRetryCommand(step spec.PlanStep, workDir string, sessionID string, feedback string) *exec.Cmd
	ParseOutput(output []byte) (DriverOutput, error)
}

// CommandRunner executes a command and returns its output.
type CommandRunner func(cmd *exec.Cmd) ([]byte, error)

// EscalationAction represents a supervisor escalation level.
type EscalationAction string

// Only RefineStep is currently implemented. Additional levels will be added
// when the supervisor gains multi-level escalation (guide → replan → abort).
const (
	EscalateRefineStep EscalationAction = "refine_step"
)

// SupervisorConfig configures the supervision loop.
type SupervisorConfig struct {
	LLM        agent.LLM
	MaxRetries int
	// AgentDefs are the supervision council agents (validator, guide, reviewer).
	// Loaded from .borg/agents/ via agent.LoadAgentDefs.
	// If nil, a default system prompt is used for validation.
	AgentDefs map[string]agent.AgentDef
}

// StepOutcome is the result of supervising a step.
type StepOutcome struct {
	Success    bool
	Attempts   int
	Files      []string
	Escalation string
}

// Supervisor orchestrates the retry-and-validate loop for a plan step.
type Supervisor struct {
	cfg    SupervisorConfig
	runner CommandRunner
}

// NewSupervisor creates a Supervisor with the given config and command runner.
func NewSupervisor(cfg SupervisorConfig, runner CommandRunner) *Supervisor {
	return &Supervisor{
		cfg:    cfg,
		runner: runner,
	}
}

// Supervise runs the full supervision loop for a plan step.
func (s *Supervisor) Supervise(ctx context.Context, step spec.PlanStep, driver AgentDriver, workDir string) (*StepOutcome, error) {
	var (
		prevOutput string
		lastOutput DriverOutput
		feedback   string
		stuck      bool
	)

	fastRetry := agent.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}

	for attempt := 1; attempt <= s.cfg.MaxRetries; attempt++ {
		// Build command.
		var cmd *exec.Cmd
		if attempt == 1 {
			cmd = driver.BuildCommand(step, workDir)
		} else {
			cmd = driver.BuildRetryCommand(step, workDir, lastOutput.SessionID, feedback)
		}

		// Run command.
		raw, err := s.runner(cmd)
		if err != nil {
			return nil, fmt.Errorf("command execution failed on attempt %d: %w", attempt, err)
		}

		// Parse output.
		parsed, err := driver.ParseOutput(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing driver output on attempt %d: %w", attempt, err)
		}
		lastOutput = parsed

		// Stuck detection: identical output to previous attempt.
		if attempt > 1 && parsed.Output == prevOutput {
			stuck = true
		}
		prevOutput = parsed.Output

		// Validate via LLM.
		validationResp, err := s.validate(ctx, step, parsed, fastRetry)
		if err != nil {
			return nil, fmt.Errorf("LLM validation on attempt %d: %w", attempt, err)
		}

		if isPass(validationResp.Content) {
			return &StepOutcome{
				Success:  true,
				Attempts: attempt,
				Files:    parsed.Files,
			}, nil
		}

		// Extract feedback from the LLM response for the next retry.
		feedback = validationResp.Content
	}

	// All retries exhausted.
	outcome := &StepOutcome{
		Success:  false,
		Attempts: s.cfg.MaxRetries,
		Files:    lastOutput.Files,
	}

	if stuck {
		outcome.Escalation = string(EscalateRefineStep)
	}

	return outcome, nil
}

// isPass checks whether the LLM validation response indicates a pass.
// It looks for "PASS" as the first word to avoid false positives from
// responses like "FAIL: tests do not pass".
func isPass(content string) bool {
	trimmed := strings.TrimSpace(content)
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, "PASS")
}

// validate asks the LLM whether the agent output satisfies the step's acceptance criteria.
// Uses the "validator" agent def if available; otherwise falls back to a default prompt.
func (s *Supervisor) validate(ctx context.Context, step spec.PlanStep, output DriverOutput, retryCfg agent.RetryConfig) (*agent.GenerateResponse, error) {
	var assertions strings.Builder
	for _, a := range step.Assertions {
		assertions.WriteString(fmt.Sprintf("- %s", string(a.Kind)))
		if a.Target != "" {
			assertions.WriteString(fmt.Sprintf(" target=%s", a.Target))
		}
		if a.Message != "" {
			assertions.WriteString(fmt.Sprintf(" (%s)", a.Message))
		}
		assertions.WriteString("\n")
	}

	userPrompt := fmt.Sprintf(
		"Step: %s\n\nAcceptance criteria:\n%s\nAgent output:\n%s\n\nDoes this output satisfy the acceptance criteria? Respond PASS or FAIL with explanation.",
		step.Description,
		assertions.String(),
		output.Output,
	)

	messages := []agent.Message{{Role: "user", Content: userPrompt}}

	// Use the validator agent def if available.
	if def, ok := s.cfg.AgentDefs["validator"]; ok {
		req := agent.BuildGenerateRequest(def, messages)
		return agent.GenerateWithRetry(ctx, s.cfg.LLM, req, retryCfg)
	}

	req := agent.GenerateRequest{Messages: messages}
	return agent.GenerateWithRetry(ctx, s.cfg.LLM, req, retryCfg)
}
