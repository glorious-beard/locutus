package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// EscalationAction represents a supervisor escalation level.
type EscalationAction string

// Only RefineStep is currently implemented. Additional levels will be added
// when the supervisor gains multi-level escalation (guide → replan → abort).
const (
	EscalateRefineStep EscalationAction = "refine_step"
)

// SupervisorConfig configures the supervision loop.
type SupervisorConfig struct {
	// LLM is the strong-tier client used for validation and the
	// permission/question guardian. Must be non-nil when the supervisor is
	// expected to validate.
	LLM agent.AgentExecutor
	// FastLLM is the fast-tier client used by the cycle-detection monitor
	// (Part 6). Keeping it separate from LLM bounds monitoring cost before
	// multi-tier routing lands. May be nil when no "monitor" agent is
	// configured; required whenever AgentDefs["monitor"] is present.
	FastLLM agent.AgentExecutor

	MaxRetries int
	// AgentDefs are the supervision council agents (validator, guide, reviewer,
	// monitor). Loaded from .borg/agents/ via agent.LoadAgentDefs. If nil,
	// a default system prompt is used for validation.
	AgentDefs map[string]agent.AgentDef
	// ProgressNotifier receives human-readable updates as the supervisor
	// observes the coding agent's event stream. Optional; a nil notifier
	// disables progress emission.
	ProgressNotifier ProgressNotifier
	// Logger is used for non-fatal supervision events (e.g., the one-time
	// INFO log when the monitor agent is unset). Nil falls back to
	// slog.Default().
	Logger *slog.Logger
}

// StepOutcome is the result of supervising a step.
//
// SessionID is the streaming-driver conversation ID surfaced from the
// last attempt's event feed. Captured here so runWorkstream can roll it
// up into WorkstreamResult.AgentSessionID for adopt to persist (DJ-074).
type StepOutcome struct {
	Success    bool
	Attempts   int
	Files      []string
	Escalation string
	SessionID  string
}

// Supervisor orchestrates the retry-and-validate loop for a plan step.
type Supervisor struct {
	cfg    SupervisorConfig
	runner CommandRunner

	// permBridge is the permission bridge for the active streaming attempt.
	// When non-nil, runAttempt merges bridge.Events into its event loop and
	// routes EventPermissionRequest to handleInteraction. When nil, the
	// supervisor does not intercept permissions — Claude runs with
	// whatever permission-mode the driver configured (see
	// ClaudeCodeDriver.BuildCommand, which defaults to acceptEdits).
	// Production wire-up creates one bridge per attempt; tests can set
	// this field directly.
	permBridge *PermBridge

	// monitorDisabledLogged ensures the "monitor agent not configured" INFO
	// log fires exactly once per supervisor, not once per attempt.
	monitorDisabledLogged sync.Once
}

// logger returns the configured logger, falling back to slog.Default().
func (s *Supervisor) logger() *slog.Logger {
	if s.cfg.Logger != nil {
		return s.cfg.Logger
	}
	return slog.Default()
}

// logMonitorDisabledOnce emits a single INFO-level notice when the monitor
// agent is not configured. See SupervisorConfig.FastLLM and AgentDefs.
func (s *Supervisor) logMonitorDisabledOnce() {
	s.monitorDisabledLogged.Do(func() {
		s.logger().Info("monitor agent not configured, cycle detection disabled")
	})
}

// NewSupervisor creates a Supervisor with the given config and command runner.
func NewSupervisor(cfg SupervisorConfig, runner CommandRunner) *Supervisor {
	return &Supervisor{
		cfg:    cfg,
		runner: runner,
	}
}

// Supervise runs the retry-and-validate loop for a plan step. Each
// attempt invokes runAttempt (the streaming event loop) and then
// validates the agent's output via the validator LLM. Intra-attempt
// *churnDetected errors short-circuit the attempt and feed into a
// sliding-window escalation rule: if ≥2 of the last 3 attempts ended
// in churn, the step is escalated to RefineStep — repeated cycling
// suggests the step itself is ill-posed, not the implementation.
func (s *Supervisor) Supervise(ctx context.Context, step spec.PlanStep, driver StreamingDriver, workDir string) (*StepOutcome, error) {
	return s.superviseImpl(ctx, step, driver, workDir, "")
}

// SuperviseFrom is the resume-aware variant of Supervise. When
// initialSessionID is non-empty, the first attempt re-attaches to the
// prior agent conversation via the streaming driver's
// BuildRetryCommand path (which translates to `--resume <id>` for
// drivers that support it). Used by runWorkstream when DJ-074 resume
// is in effect; equivalent to Supervise when initialSessionID is empty.
func (s *Supervisor) SuperviseFrom(ctx context.Context, step spec.PlanStep, driver StreamingDriver, workDir, initialSessionID string) (*StepOutcome, error) {
	return s.superviseImpl(ctx, step, driver, workDir, initialSessionID)
}

func (s *Supervisor) superviseImpl(ctx context.Context, step spec.PlanStep, driver StreamingDriver, workDir, initialSessionID string) (*StepOutcome, error) {
	fastRetry := agent.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}

	var (
		sessionID = initialSessionID
		feedback  string
		outcomes  []outcomeKind
		lastFiles []string
	)

	for attempt := 1; attempt <= s.cfg.MaxRetries; attempt++ {
		result, err := s.runAttempt(ctx, step, driver, workDir, sessionID, feedback)

		// Preserve anything the attempt produced so we can surface files
		// and resume context in subsequent attempts, even if this one
		// aborted.
		if result != nil {
			if result.sessionID != "" {
				sessionID = result.sessionID
			}
			if len(result.files) > 0 {
				lastFiles = result.files
			}
		}

		// --- Intra-attempt abort signals ---

		if churnErr, ok := err.(*churnDetected); ok {
			outcomes = append(outcomes, outcomeChurn)
			if churnCountInLastN(outcomes, 3) >= 2 {
				return &StepOutcome{
					Success:    false,
					Attempts:   attempt,
					Files:      lastFiles,
					Escalation: string(EscalateRefineStep),
					SessionID:  sessionID,
				}, nil
			}
			feedback = fmt.Sprintf(
				"Previous attempt cycled (%s): %s. Do not repeat the same approach.",
				churnErr.pattern, churnErr.reasoning,
			)
			continue
		}

		if err != nil {
			// Stream parse or runner errors: treat as attempt failure,
			// surface the error as feedback for the next attempt.
			outcomes = append(outcomes, outcomeError)
			feedback = err.Error()
			continue
		}

		// --- Normal validation path ---

		validationResp, verr := s.validate(ctx, step, result.finalText, fastRetry)
		if verr != nil {
			return nil, fmt.Errorf("LLM validation on attempt %d: %w", attempt, verr)
		}

		if isPass(validationResp.Content) {
			return &StepOutcome{
				Success:   true,
				Attempts:  attempt,
				Files:     result.files,
				SessionID: sessionID,
			}, nil
		}

		outcomes = append(outcomes, outcomeValidationFail)
		feedback = validationResp.Content
	}

	// Retries exhausted without a passing attempt. No escalation unless
	// the churn sliding window already triggered above (which would have
	// returned before now).
	return &StepOutcome{
		Success:   false,
		Attempts:  s.cfg.MaxRetries,
		Files:     lastFiles,
		SessionID: sessionID,
	}, nil
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
// agentOutput is the final text the agent produced (typically the accumulated
// EventResult text from runAttempt).
func (s *Supervisor) validate(ctx context.Context, step spec.PlanStep, agentOutput string, retryCfg agent.RetryConfig) (*agent.AgentOutput, error) {
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
		"Step: %s\n\nAcceptance criteria:\n%s\nAgent output:\n%s\n\nEvaluate this output.",
		step.Description,
		assertions.String(),
		agentOutput,
	)

	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: userPrompt}}}

	// Use the validator agent def if available.
	if def, ok := s.cfg.AgentDefs["validator"]; ok {
		return agent.RunWithRetry(ctx, s.cfg.LLM, def, input, retryCfg)
	}

	return agent.RunWithRetry(ctx, s.cfg.LLM, agent.AgentDef{ID: "validator"}, input, retryCfg)
}
