package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch/policy"
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
	// PolicyForWorkstream returns the permission policy applied to all
	// attempts of the given workstream. Called once per Supervise
	// invocation under DJ-121's coarsening; the returned Policy is threaded
	// into every conn.Prompt for that workstream's retries. When nil, the
	// supervisor falls back to policy.AllowOncePolicy — the same placeholder
	// used in tests, suitable for early-integration runs before the
	// production guardian is wired in. The cmd layer in production sets
	// this to a closure that constructs a fresh guardian.Guardian per
	// workstream (see DJ-119 Phase 4).
	PolicyForWorkstream func(ws spec.Workstream) policy.Policy
}

// StepOutcome is the result of supervising a step.
//
// SessionID is the ACP session ID the workstream's Connection used while
// running this step. Captured here so runWorkstream can roll it up into
// WorkstreamResult.AgentSessionID for adopt to persist (DJ-074). Under
// the ACP lifecycle (DJ-119), all attempts of all steps in a workstream
// share the same sessionID — the field is preserved per-step for
// compatibility with the WorkstreamResult contract.
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

// policyForWorkstream resolves the per-workstream permission policy. When
// SupervisorConfig.PolicyForWorkstream is unset, falls back to
// policy.AllowOncePolicy — the placeholder behaviour that lets tests
// and early-integration runs proceed without a configured guardian.
func (s *Supervisor) policyForWorkstream(ws spec.Workstream) policy.Policy {
	if s.cfg.PolicyForWorkstream == nil {
		return policy.AllowOncePolicy{}
	}
	if p := s.cfg.PolicyForWorkstream(ws); p != nil {
		return p
	}
	return policy.AllowOncePolicy{}
}

// NewSupervisor creates a Supervisor with the given config and command runner.
//
// CommandRunner is retained on the Supervisor for non-ACP code paths (e.g.,
// the validator LLM call, which uses the agent.AgentExecutor directly).
// Under DJ-119 the coding-agent transport is acp.Connection, plumbed
// through Supervise's PromptConn argument — not through the runner.
func NewSupervisor(cfg SupervisorConfig, runner CommandRunner) *Supervisor {
	return &Supervisor{
		cfg:    cfg,
		runner: runner,
	}
}

// Supervise runs the retry-and-validate loop for one workstream over an
// already-open ACP connection. Each attempt issues one Prompt on the shared
// sessionID asking the agent to execute the workstream end-to-end against
// its worktree-resident plan; failed attempts feed validator-derived
// feedback into the next Prompt on the SAME session, so the agent retains
// conversation context across attempts (the per-attempt session/resume the
// pre-DJ-119 driver model used is now automatic).
//
// Per DJ-121, the supervised unit is the workstream, not the PlanStep. The
// agent owns its own internal step decomposition via the
// _locutus/checklist.md it maintains in the worktree; Locutus's
// retry/validate loop grades the workstream's overall acceptance criteria
// at completion.
//
// Intra-attempt *churnDetected errors short-circuit the attempt and feed
// into a sliding-window escalation rule: if ≥2 of the last 3 attempts
// ended in churn, the workstream is escalated to RefineStep — repeated
// cycling suggests the workstream itself is ill-posed, not the
// implementation.
func (s *Supervisor) Supervise(ctx context.Context, ws spec.Workstream, conn PromptConn, sessionID string) (*StepOutcome, error) {
	fastRetry := agent.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}

	pol := s.policyForWorkstream(ws)

	var (
		feedback  string
		outcomes  []outcomeKind
		lastFiles []string
	)

	for attempt := 1; attempt <= s.cfg.MaxRetries; attempt++ {
		result, err := s.runAttempt(ctx, ws, conn, sessionID, feedback, pol)

		// Preserve anything the attempt produced so we can surface files in
		// subsequent attempts, even if this one aborted.
		if result != nil && len(result.files) > 0 {
			lastFiles = result.files
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
			// Prompt or stream errors: treat as attempt failure, surface the
			// error as feedback for the next attempt.
			outcomes = append(outcomes, outcomeError)
			feedback = err.Error()
			continue
		}

		// --- Normal validation path ---

		validationResp, verr := s.validate(ctx, ws, result.finalText, fastRetry)
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

// validate asks the LLM whether the agent output satisfies the workstream's
// acceptance criteria. Uses the "validator" agent def if available;
// otherwise falls back to a default prompt. agentOutput is the final text
// the agent produced (typically the accumulated EventResult text from
// runAttempt).
//
// DJ-121 contract: acceptance criteria live on Workstream.Assertions.
// Under the soft-deprecate transition path, this function prefers
// ws.Assertions when populated and falls back to the union of every
// step's assertions only when ws.Assertions is empty — covering the
// case where the planner agent (Phase 6's update) hasn't yet been
// migrated to emit workstream-level criteria. Phase 9 removes the
// fallback once all planner outputs carry ws.Assertions.
func (s *Supervisor) validate(ctx context.Context, ws spec.Workstream, agentOutput string, retryCfg agent.RetryConfig) (*agent.AgentOutput, error) {
	var assertions strings.Builder
	writeAssertion := func(a spec.Assertion) {
		assertions.WriteString(fmt.Sprintf("- %s", string(a.Kind)))
		if a.Target != "" {
			assertions.WriteString(fmt.Sprintf(" target=%s", a.Target))
		}
		if a.Message != "" {
			assertions.WriteString(fmt.Sprintf(" (%s)", a.Message))
		}
		assertions.WriteString("\n")
	}
	switch {
	case len(ws.Assertions) > 0:
		for _, a := range ws.Assertions {
			writeAssertion(a)
		}
	default:
		// Transition fallback: planner hasn't been migrated to emit
		// workstream-level assertions yet. Union the step assertions so
		// validation still runs against the criteria the planner did
		// produce. Phase 9 deletes this branch.
		for _, step := range ws.Steps {
			for _, a := range step.Assertions {
				writeAssertion(a)
			}
		}
	}

	userPrompt := fmt.Sprintf(
		"Workstream: %s (%s)\n\nAcceptance criteria:\n%s\nAgent output:\n%s\n\nEvaluate whether the workstream is complete and the acceptance criteria hold.",
		ws.ID,
		ws.StrategyDomain,
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
