package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/pterm/pterm"
)

// cliSink renders council progress as one printed line per state
// change on stderr. Each event prints a colored status prefix, the
// step/agent label, and (for completions) elapsed time.
//
// We previously rendered with pterm's MultiPrinter + Spinner machinery,
// one spinner per agent with active animation. That works fine for a
// linear workflow with one in-flight call at a time, but Phase 3
// fanout fires N concurrent goroutines whose spinners overlap in the
// MultiPrinter's redraw loop. With WithShowTimer enabled, the result
// is a thrashing redraw that accumulates duplicate "SUCCESS" lines as
// new spinners are added. The reliable shape for parallel work is
// one-line-per-event without active animation.
type cliSink struct {
	w       io.Writer
	mu      sync.Mutex
	starts  map[string]time.Time
	closed  bool
	pending map[string]struct{} // keys that have emitted started but not completed/error/skipped
}

// newCLISink returns a sink ready to receive events. Caller must
// invoke Close to surface any still-pending items at shutdown.
func newCLISink() *cliSink {
	return &cliSink{
		w:       os.Stderr,
		starts:  map[string]time.Time{},
		pending: map[string]struct{}{},
	}
}

// plainSink renders council progress as one structured log line per
// event on stderr — the --plain fallback. No ANSI, no cursor moves;
// safe for any consumer.
type plainSink struct {
	w  io.Writer
	mu sync.Mutex
}

func newPlainSink() *plainSink {
	return &plainSink{w: os.Stderr}
}

func (s *plainSink) OnEvent(e agent.WorkflowEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := e.Timestamp.Format("15:04:05")
	fmt.Fprintf(s.w, "%s  %-9s  step=%s agent=%s",
		ts, strings.ToUpper(e.Status), e.StepID, e.AgentID)
	if e.Message != "" {
		fmt.Fprintf(s.w, "  %s", e.Message)
	}
	fmt.Fprintln(s.w)
}

func (s *plainSink) Close() {}

func (s *cliSink) key(e agent.WorkflowEvent) string {
	if e.AgentID != "" {
		return e.StepID + "/" + e.AgentID
	}
	return e.StepID
}

func (s *cliSink) label(e agent.WorkflowEvent) string {
	if e.AgentID != "" {
		return fmt.Sprintf("%s · %s", e.StepID, e.AgentID)
	}
	return e.StepID
}

// OnEvent prints a single line per state transition. Per-agent events
// only — workflow-level events (no AgentID) are dropped because their
// stepID-only labels would clutter without naming the actor.
//
// Lifecycle:
//   - queued    → " QUEUED " (item is waiting on the per-model
//                  concurrency throttle)
//   - started   → "RUNNING " (call has left the queue and is hitting
//                  the provider)
//   - completed → "SUCCESS " (with elapsed time relative to started)
//   - error     → " ERROR  " (with the error message)
//   - skipped   → "SKIPPED "
//   - retrying  → "RETRYING"
func (s *cliSink) OnEvent(e agent.WorkflowEvent) {
	if e.AgentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	key := s.key(e)
	label := s.label(e)
	switch e.Status {
	case "queued":
		s.starts[key] = e.Timestamp
		s.pending[key] = struct{}{}
		s.print("QUEUED  ", pterm.NewStyle(pterm.FgGray, pterm.Bold), label, "")
	case "started":
		// Mark started time when no prior queued event was seen (e.g.
		// callers that emit "started" directly). Otherwise preserve
		// the queued timestamp so elapsed includes wait time — the
		// operator usually wants total wall-clock, not provider time.
		if _, ok := s.starts[key]; !ok {
			s.starts[key] = e.Timestamp
		}
		s.pending[key] = struct{}{}
		s.print("RUNNING ", pterm.NewStyle(pterm.FgCyan, pterm.Bold), label, "")
	case "completed":
		delete(s.pending, key)
		s.print("SUCCESS ", pterm.NewStyle(pterm.FgGreen, pterm.Bold), label, s.elapsed(key, e.Timestamp))
	case "error":
		delete(s.pending, key)
		extra := s.elapsed(key, e.Timestamp)
		if e.Message != "" {
			if extra != "" {
				extra = extra + " — " + e.Message
			} else {
				extra = "— " + e.Message
			}
		}
		s.print(" ERROR  ", pterm.NewStyle(pterm.FgRed, pterm.Bold), label, extra)
	case "skipped":
		delete(s.pending, key)
		s.print("SKIPPED ", pterm.NewStyle(pterm.FgYellow, pterm.Bold), label, "")
	case "retrying":
		extra := ""
		if e.Message != "" {
			extra = "— " + e.Message
		}
		s.print("RETRYING", pterm.NewStyle(pterm.FgYellow, pterm.Bold), label, extra)
	}
}

// print writes one status line. Caller must hold s.mu.
func (s *cliSink) print(prefix string, style *pterm.Style, label, extra string) {
	colored := style.Sprintf(" %s ", prefix)
	if extra != "" {
		fmt.Fprintf(s.w, "%s %s %s\n", colored, label, extra)
	} else {
		fmt.Fprintf(s.w, "%s %s\n", colored, label)
	}
}

// elapsed renders a parenthesised "(Xs)" elapsed time relative to the
// recorded start of this key. Returns empty when no start is tracked.
func (s *cliSink) elapsed(key string, completedAt time.Time) string {
	t, ok := s.starts[key]
	if !ok {
		return ""
	}
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	return "(" + completedAt.Sub(t).Round(time.Second).String() + ")"
}

// Close prints a final line for any keys that never reached a terminal
// state (completed/error/skipped). Helps surface in-flight work that
// got cut off by a panic / signal.
func (s *cliSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	style := pterm.NewStyle(pterm.FgRed, pterm.Bold)
	for key := range s.pending {
		s.print("INTERRUPT", style, key, s.elapsed(key, time.Now()))
	}
	s.closed = true
}
