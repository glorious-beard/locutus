package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/chetan/locutus/internal/spec"
)

// StreamingDriver is the supervisor-facing contract for coding-agent drivers
// that support event-streamed supervision. Concrete drivers in
// `internal/dispatch/drivers` satisfy this via structural typing — they
// also implement the batch `drivers.AgentDriver` interface for backward
// compatibility.
type StreamingDriver interface {
	BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd
	BuildRetryCommand(step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd
	ParseStream(r io.Reader) StreamParser
	RespondToAgent(sessionID, response string) (*exec.Cmd, error)
}

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

// runAttempt runs one event-streamed invocation of the coding agent and
// returns the accumulated result. It is a pure event loop — monitor
// integration (Part 6) and permission-bridge merging (Part 7) hook in
// later. For now, Next errors and ctx cancellation propagate directly;
// io.EOF ends the loop cleanly.
func (s *Supervisor) runAttempt(
	ctx context.Context,
	step spec.PlanStep,
	driver StreamingDriver,
	workDir, sessionID, feedback string,
) (*attemptResult, error) {
	cmd := s.buildAttemptCommand(driver, step, workDir, sessionID, feedback)

	stream, err := s.runner(cmd)
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	defer func() { _ = stream.Close() }()

	parser := driver.ParseStream(stream)
	defer func() { _ = parser.Close() }()

	result := &attemptResult{}
	mon := newMonitor()

	for {
		evt, err := parser.Next(ctx)
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, err
		}

		result.accumulate(evt)
		s.emitProgress(ctx, evt)
		mon.Observe(evt)

		if mon.ShouldCheck() {
			verdict, cerr := s.monitorCycle(ctx, step, mon.RecentEvents())
			mon.MarkChecked(cerr)
			if cerr != nil {
				// Circuit breaker will suppress further checks if this
				// keeps happening; proceed with the stream in the meantime.
				continue
			}
			if verdict.IsCycle && verdict.Confidence >= 0.7 {
				return result, &churnDetected{
					pattern:   verdict.Pattern,
					reasoning: verdict.Reasoning,
				}
			}
		}

		// Permission/question handling (Part 7) will be added here.
	}
}

func (s *Supervisor) buildAttemptCommand(driver StreamingDriver, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	if sessionID == "" {
		return driver.BuildCommand(step, workDir)
	}
	return driver.BuildRetryCommand(step, workDir, sessionID, feedback)
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
	case EventPermissionRequest:
		if evt.ToolName != "" {
			return fmt.Sprintf("Agent wants permission: %s", evt.ToolName)
		}
		return "Agent wants permission"
	case EventClarifyQuestion:
		if evt.Text != "" {
			return fmt.Sprintf("Agent asked: %s", evt.Text)
		}
		return "Agent asked a clarifying question"
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
