package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/pterm/pterm"
)

// cliSink renders council progress as one pterm spinner per agent on
// stderr. Spinners on stderr is the convention for progress UIs (cargo,
// docker, npm) and means a stdout pipe stays clean.
type cliSink struct {
	multi    *pterm.MultiPrinter
	spinners map[string]*pterm.SpinnerPrinter // keyed by stepID/agentID
	order    []string                         // insertion order for stable display
	starts   map[string]time.Time
	mu       sync.Mutex
	started  bool
}

// newCLISink starts a pterm MultiPrinter on stderr and returns a sink
// ready to receive events. Caller must invoke Close to tear down the UI.
func newCLISink() *cliSink {
	mp := pterm.DefaultMultiPrinter.WithWriter(os.Stderr)
	mp, _ = mp.Start()
	return &cliSink{
		multi:    mp,
		spinners: map[string]*pterm.SpinnerPrinter{},
		starts:   map[string]time.Time{},
		started:  true,
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

// OnEvent updates the spinner state for the agent referenced in the
// event. Called from a single bridge goroutine in GenerateSpec, but
// guarded with a mutex anyway since pterm spinners hold their own
// state and a future caller might fan out.
//
// Per-agent only. Workflow-level events (iteration markers with no
// stepID, DAG step lifecycle events with no agentID, convergence
// completions without a paired started) are skipped — they would
// either render as orphan spinners that Close() then marks as
// "interrupted", or as redundant duplicates of the per-agent line.
// plainSink still receives the full stream because structured logs
// cope fine with the extra detail.
//
// Lifecycle: a typical workflow agent emits "queued" → "started" →
// "completed". Queued creates the spinner with a "queued" label so
// items waiting on the per-model concurrency throttle look distinct
// from items hitting the provider. Started updates the same spinner
// to a running label. Direct "started" without a prior "queued" still
// creates a fresh spinner — preserves backward compatibility with
// callers that haven't adopted the queued event.
func (s *cliSink) OnEvent(e agent.WorkflowEvent) {
	if e.AgentID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.key(e)
	switch e.Status {
	case "queued":
		if _, exists := s.spinners[key]; exists {
			return
		}
		sp, err := pterm.DefaultSpinner.
			WithWriter(s.multi.NewWriter()).
			WithShowTimer(true).
			WithRemoveWhenDone(false).
			Start(fmt.Sprintf("%s · queued", s.label(e)))
		if err != nil {
			return
		}
		s.spinners[key] = sp
		s.starts[key] = e.Timestamp
		s.order = append(s.order, key)
	case "started":
		if sp, ok := s.spinners[key]; ok {
			// Pre-existing spinner from a "queued" event → flip to
			// running by dropping the queued suffix.
			sp.UpdateText(s.label(e))
			return
		}
		sp, err := pterm.DefaultSpinner.
			WithWriter(s.multi.NewWriter()).
			WithShowTimer(true).
			WithRemoveWhenDone(false).
			Start(s.label(e))
		if err != nil {
			return
		}
		s.spinners[key] = sp
		s.starts[key] = e.Timestamp
		s.order = append(s.order, key)
	case "completed":
		if sp, ok := s.spinners[key]; ok {
			sp.Success(s.label(e))
		}
	case "error":
		if sp, ok := s.spinners[key]; ok {
			msg := s.label(e)
			if e.Message != "" {
				msg = fmt.Sprintf("%s — %s", msg, e.Message)
			}
			sp.Fail(msg)
		}
	case "skipped":
		if sp, ok := s.spinners[key]; ok {
			sp.Warning(fmt.Sprintf("%s (skipped)", s.label(e)))
		}
	case "retrying":
		if sp, ok := s.spinners[key]; ok {
			sp.UpdateText(fmt.Sprintf("%s · retrying", s.label(e)))
		}
	}
}

// Close stops every still-running spinner and tears down the
// MultiPrinter. Safe to call once. Spinners that never received a
// completed/error event are marked as failed so the UI never leaves
// a phantom in-flight indicator.
func (s *cliSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	// Sort orphan spinners by insertion order for predictable output.
	keys := make([]string, 0, len(s.order))
	keys = append(keys, s.order...)
	sort.SliceStable(keys, func(i, j int) bool { return i < j })
	for _, k := range keys {
		sp := s.spinners[k]
		if sp != nil && sp.IsActive {
			sp.Fail(fmt.Sprintf("%s · interrupted", k))
		}
	}
	_, _ = s.multi.Stop()
	s.started = false
}
