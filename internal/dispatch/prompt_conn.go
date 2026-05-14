package dispatch

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/dispatch/policy"
	"github.com/chetan/locutus/internal/spec"
)

// PromptConn is the supervisor-facing slice of an ACP connection. It is
// declared on the consumer side (not imported from the acp package) so
// dispatch doesn't depend on acp directly for the transport type — only
// for the provider-neutral `dispatch/policy` types that Prompt threads
// through. The actual `acp.Connection` satisfies this interface
// structurally; tests substitute a `fakePromptConn`.
//
// One PromptConn is opened per workstream and shared across all steps
// and retry attempts — the DJ-119 spawn-per-workstream lifecycle. The
// Policy passed to Prompt is per-Prompt (and in practice per-step under
// the Phase 4 wiring); the supervisor passes a fresh guardian per
// Supervise call so the policy's view of the current step is correct.
type PromptConn interface {
	// NewSession creates a fresh conversation session on the underlying
	// agent. cwd must be an absolute path (the worktree root, in
	// production). Returns the session id; future Prompt/Cancel calls
	// reference it.
	NewSession(ctx context.Context, cwd string) (sessionID string, err error)

	// Prompt sends one prompt turn on sessionID and returns a channel of
	// streaming events that closes when the turn is over. The terminal
	// event is EventResult on success or EventError on failure. pol is
	// the permission policy applied to any `session/request_permission`
	// the agent makes during this turn; nil cancels every permission
	// request.
	Prompt(ctx context.Context, sessionID, text string, pol policy.Policy) (<-chan AgentEvent, error)

	// Cancel asks the agent to abort the in-flight prompt turn on
	// sessionID. Used when the supervisor's ctx is cancelled.
	Cancel(ctx context.Context, sessionID string) error

	// Close terminates the underlying agent subprocess. Called when the
	// workstream finishes (success or failure).
	Close() error
}

// buildPromptText constructs the user-message text sent to the agent for
// one attempt. On the first attempt (feedback == ""), this is the
// workstream's kick-off prompt: a short directive pointing the agent at
// its worktree-resident plan (_locutus/plan.md), where the full
// description, acceptance criteria, and the checklist instruction live.
// On retry, feedback carries the validator's reasoning and replaces the
// kick-off — the agent is still in the same ACP session, so the original
// workstream framing is already in its conversation context AND in the
// plan.md it can re-read at any time.
//
// Under DJ-121's coarsening: one prompt per workstream (not per step).
// The agent decomposes the workstream into its own steps and tracks them
// in _locutus/checklist.md as it works.
func buildPromptText(ws spec.Workstream, feedback string) string {
	if feedback != "" {
		return feedback
	}
	return fmt.Sprintf(
		"You have been assigned workstream %q (strategy domain: %s).\n\n"+
			"Your full briefing — description, acceptance criteria, and a suggested decomposition — is in `_locutus/plan.md` at the worktree root. Read it first.\n\n"+
			"As you work, maintain `_locutus/checklist.md` to track your own progress: plan your decomposition, mark steps as you complete them, and record any decisions you made that aren't already in the spec DAG.\n\n"+
			"When the workstream is complete, summarize what landed.",
		ws.ID, ws.StrategyDomain,
	)
}
