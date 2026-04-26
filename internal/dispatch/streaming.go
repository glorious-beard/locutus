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
	BuildCommand(ctx context.Context, step spec.PlanStep, workDir string) *exec.Cmd
	BuildRetryCommand(ctx context.Context, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd
	ParseStream(r io.Reader) StreamParser
	RespondToAgent(ctx context.Context, sessionID, response string) (*exec.Cmd, error)
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

// streamResult carries one pull from parser.Next to the main event loop.
type streamResult struct {
	evt AgentEvent
	err error
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

// runAttempt runs one event-streamed invocation of the coding agent and
// returns the accumulated result. The event loop merges two sources:
// the stream parser (driver output on stdout) and the permission bridge
// (out-of-band, via Unix socket from a subprocess Claude spawns for
// --permission-prompt-tool). When no bridge is attached the merge is
// a no-op (a nil channel never fires in select).
func (s *Supervisor) runAttempt(
	ctx context.Context,
	step spec.PlanStep,
	driver StreamingDriver,
	workDir, sessionID, feedback string,
) (*attemptResult, error) {
	// Inner cancelable ctx so that an early return from this function also
	// tears down the parser pump goroutine.
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	defer cancelAttempt()

	cmd := s.buildAttemptCommand(attemptCtx, driver, step, workDir, sessionID, feedback)

	stream, err := s.runner(cmd)
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	defer func() { _ = stream.Close() }()

	parser := driver.ParseStream(stream)
	defer func() { _ = parser.Close() }()

	parserEvents := pumpParser(attemptCtx, parser)

	result := &attemptResult{}
	mon := newMonitor()

	for {
		var evt AgentEvent
		select {
		case r, ok := <-parserEvents:
			if !ok {
				// Parser goroutine exited without sending a terminal
				// result — shouldn't happen, but treat as clean end.
				return result, nil
			}
			if errors.Is(r.err, io.EOF) {
				return result, nil
			}
			if r.err != nil {
				return result, r.err
			}
			evt = r.evt
		case bevt, ok := <-s.bridgeEvents():
			if !ok {
				// Bridge closed; continue with just the parser stream.
				continue
			}
			evt = bevt
		case <-ctx.Done():
			return result, ctx.Err()
		}

		result.accumulate(evt)
		s.emitProgress(ctx, evt)
		mon.Observe(evt)

		switch evt.Kind {
		case EventPermissionRequest, EventClarifyQuestion:
			if err := s.handleInteraction(ctx, step, evt); err != nil {
				return result, err
			}
			// No resume needed — the bridge lets Claude continue once it
			// receives the decision. Move on to the next event.
			continue
		}

		if mon.ShouldCheck() {
			verdict, cerr := s.monitorCycle(ctx, step, mon.RecentEvents())
			mon.MarkChecked(cerr)
			if cerr != nil {
				continue
			}
			if verdict.IsCycle && verdict.Confidence >= 0.7 {
				return result, &churnDetected{
					pattern:   verdict.Pattern,
					reasoning: verdict.Reasoning,
				}
			}
		}
	}
}

// pumpParser drains parser.Next on a goroutine so the main event loop can
// select between parser events and bridge events. The goroutine exits
// when parser returns any error (including io.EOF) or when ctx is
// canceled. The returned channel closes when the goroutine exits.
func pumpParser(ctx context.Context, parser StreamParser) <-chan streamResult {
	out := make(chan streamResult, 1)
	go func() {
		defer close(out)
		for {
			evt, err := parser.Next(ctx)
			select {
			case out <- streamResult{evt: evt, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

// bridgeEvents returns the bridge events channel if a bridge is attached,
// or nil otherwise. A nil channel blocks forever in select, which is
// exactly the "no bridge" behavior we want.
func (s *Supervisor) bridgeEvents() <-chan AgentEvent {
	if s.permBridge == nil {
		return nil
	}
	return s.permBridge.Events
}

func (s *Supervisor) buildAttemptCommand(ctx context.Context, driver StreamingDriver, step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	if sessionID == "" {
		return driver.BuildCommand(ctx, step, workDir)
	}
	return driver.BuildRetryCommand(ctx, step, workDir, sessionID, feedback)
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
