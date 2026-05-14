package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/chetan/locutus/internal/dispatch/policy"
	"github.com/chetan/locutus/internal/spec"
)

// ProgressNotifier lets the supervisor emit human-readable progress updates
// to an external observer (e.g., the MCP client that invoked the tool). A
// nil notifier disables progress emission.
type ProgressNotifier interface {
	Notify(ctx context.Context, params ProgressParams) error
}

// ProgressParams carries one progress update. Token is reserved for future
// correlation with MCP progress tokens; Current/Total are reserved for
// quantified progress when we wire step-level counting.
type ProgressParams struct {
	Token   string
	Message string
	Current int
	Total   int
}

// attemptResult accumulates state across a single event-streamed attempt.
// It is produced by runAttempt; the retry loop inspects finalText and files
// to build the StepOutcome and feed validate().
type attemptResult struct {
	events    []AgentEvent
	finalText string
	files     []string
	sessionID string
}

func (r *attemptResult) accumulate(evt AgentEvent) {
	r.events = append(r.events, evt)
	if evt.SessionID != "" {
		r.sessionID = evt.SessionID
	}
	for _, p := range evt.FilePaths {
		if !containsString(r.files, p) {
			r.files = append(r.files, p)
		}
	}
	if evt.Kind == EventResult && evt.Text != "" {
		r.finalText = evt.Text
	}
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// outcomeKind tags each attempt's outcome for the sliding-window churn
// rule in Supervise. See churnCountInLastN.
type outcomeKind int

const (
	outcomeValidationFail outcomeKind = iota
	outcomeChurn
	outcomeError
)

// churnCountInLastN counts how many of the last n elements of outcomes
// are outcomeChurn. Shorter slices count what's there. Used to decide
// whether to escalate to RefineStep: ≥2 of the last 3 attempts churning
// means the step itself is likely the problem, not the implementation.
func churnCountInLastN(outcomes []outcomeKind, n int) int {
	start := len(outcomes) - n
	if start < 0 {
		start = 0
	}
	count := 0
	for _, o := range outcomes[start:] {
		if o == outcomeChurn {
			count++
		}
	}
	return count
}

// runAttempt issues one Prompt on the shared ACP sessionID and drains
// events until the channel closes. Under DJ-119 the channel always
// terminates with either EventResult (success, carrying stopReason as Text)
// or EventError (failure). Mid-stream events are observed by the monitor
// (cycle detection) and surfaced as progress notifications; permission
// requests come through policy.Policy injected into Prompt, not as inline
// events on the channel.
//
// pol is the per-workstream Policy resolved by Supervise (typically the
// Guardian impl built around the validator LLM, but falls back to
// policy.AllowOncePolicy when no PolicyForWorkstream is configured).
func (s *Supervisor) runAttempt(
	ctx context.Context,
	ws spec.Workstream,
	conn PromptConn,
	sessionID, feedback string,
	pol policy.Policy,
) (*attemptResult, error) {
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	defer cancelAttempt()

	text := buildPromptText(ws, feedback)
	events, err := conn.Prompt(attemptCtx, sessionID, text, pol)
	if err != nil {
		return nil, fmt.Errorf("acp.Prompt: %w", err)
	}

	result := &attemptResult{sessionID: sessionID}
	mon := newMonitor()

	for {
		select {
		case <-ctx.Done():
			// Best-effort cancel; the agent will surface stopReason=cancelled
			// as the terminal event, which we drop on the floor because the
			// caller has already given up on this attempt.
			_ = conn.Cancel(context.Background(), sessionID)
			return result, ctx.Err()
		case evt, ok := <-events:
			if !ok {
				// Channel closed without an EventResult/EventError terminal.
				// Per the acp.Connection contract this shouldn't happen, but
				// treat it as a clean end so the validator sees what we've
				// accumulated.
				return result, nil
			}

			if evt.Kind == EventError {
				return result, errors.New(evt.Text)
			}

			result.accumulate(evt)
			s.emitProgress(ctx, evt)
			mon.Observe(evt)

			if mon.ShouldCheck() {
				verdict, cerr := s.monitorCycle(ctx, ws, mon.RecentEvents())
				mon.MarkChecked(cerr)
				if cerr == nil && verdict.IsCycle && verdict.Confidence >= 0.7 {
					return result, &churnDetected{
						pattern:   verdict.Pattern,
						reasoning: verdict.Reasoning,
					}
				}
			}

			if evt.Kind == EventResult {
				return result, nil
			}
		}
	}
}

// emitProgress translates an AgentEvent into a ProgressParams update when
// the event is supervision-relevant. Noise (raw text deltas, api retries,
// init/result lifecycle) is suppressed — see the plan's "Events that get
// forwarded" list in Part 9.
func (s *Supervisor) emitProgress(ctx context.Context, evt AgentEvent) {
	if s.cfg.ProgressNotifier == nil {
		return
	}
	msg := progressMessage(evt)
	if msg == "" {
		return
	}
	// Best-effort: a notifier error shouldn't abort supervision.
	_ = s.cfg.ProgressNotifier.Notify(ctx, ProgressParams{Message: msg})
}

func progressMessage(evt AgentEvent) string {
	switch evt.Kind {
	case EventToolCall:
		if len(evt.FilePaths) > 0 {
			return fmt.Sprintf("Agent %s: %s", evt.ToolName, joinPaths(evt.FilePaths))
		}
		if evt.ToolName != "" {
			return fmt.Sprintf("Agent tool call: %s", evt.ToolName)
		}
	case EventError:
		if evt.Text != "" {
			return fmt.Sprintf("Agent error: %s", evt.Text)
		}
		return "Agent error"
	}
	return ""
}

func joinPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) == 1 {
		return paths[0]
	}
	out := paths[0]
	for _, p := range paths[1:] {
		out += ", " + p
	}
	return out
}
