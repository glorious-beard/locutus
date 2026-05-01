package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/stretchr/testify/assert"
)

// newCLISinkForTest constructs a cliSink whose output writes to the
// supplied buffer instead of stderr — keeps test output clean and lets
// assertions inspect the rendered lines directly.
func newCLISinkForTest(w *bytes.Buffer) *cliSink {
	return &cliSink{
		w:       w,
		starts:  map[string]time.Time{},
		pending: map[string]struct{}{},
	}
}

func TestCLISinkIgnoresWorkflowLevelEvents(t *testing.T) {
	// Regression: workflow.go emits a lone "started" iteration
	// marker with empty StepID/AgentID, and DAG step events with
	// empty AgentID. cliSink should ignore both — they have no
	// per-agent label and would render misleading lines.
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)
	t.Cleanup(func() { s.Close() })

	now := time.Now()
	s.OnEvent(agent.WorkflowEvent{Status: "started", Timestamp: now, Message: "iteration 1/1"})
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", Status: "started", Timestamp: now})
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", Status: "completed", Timestamp: now})

	assert.Empty(t, buf.String(),
		"workflow-level events without an AgentID must not produce any output")
}

func TestCLISinkRendersAgentLifecycleOnce(t *testing.T) {
	// Regression: the prior pterm Spinner + MultiPrinter renderer
	// produced repeated SUCCESS lines for the same key when many
	// concurrent spinners were active. The plain renderer must emit
	// each transition exactly once.
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)
	t.Cleanup(func() { s.Close() })

	now := time.Now()
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "queued", Timestamp: now})
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "started", Timestamp: now.Add(100 * time.Millisecond)})
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "completed", Timestamp: now.Add(2 * time.Second)})

	out := buf.String()
	// Each transition prints exactly once.
	assert.Equal(t, 1, strings.Count(out, "QUEUED"),
		"queued event should produce exactly one line")
	assert.Equal(t, 1, strings.Count(out, "RUNNING"),
		"started event should produce exactly one RUNNING line")
	assert.Equal(t, 1, strings.Count(out, "SUCCESS"),
		"completed event should produce exactly one SUCCESS line — repeated SUCCESS lines were the prior renderer's bug")
	// Elapsed time relative to queued (wall-clock, not just provider time).
	assert.Contains(t, out, "(2s)")
	// Step + agent label appears in each line.
	assert.True(t, strings.Count(out, "survey · spec_scout") >= 3,
		"each line should label step · agent")
}

func TestCLISinkRendersFanoutItemsIndependently(t *testing.T) {
	// Phase 3 fanout uses per-item stepIDs like
	// "elaborate_features (feat-x)". Each per-item key gets its own
	// state machine — completing one doesn't affect siblings.
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)
	t.Cleanup(func() { s.Close() })

	now := time.Now()
	for _, id := range []string{"feat-a", "feat-b", "feat-c"} {
		s.OnEvent(agent.WorkflowEvent{
			StepID:    "elaborate_features (" + id + ")",
			AgentID:   "spec_feature_elaborator",
			Status:    "queued",
			Timestamp: now,
		})
	}
	for _, id := range []string{"feat-a", "feat-b", "feat-c"} {
		s.OnEvent(agent.WorkflowEvent{
			StepID:    "elaborate_features (" + id + ")",
			AgentID:   "spec_feature_elaborator",
			Status:    "completed",
			Timestamp: now.Add(time.Second),
		})
	}

	out := buf.String()
	for _, id := range []string{"feat-a", "feat-b", "feat-c"} {
		label := "elaborate_features (" + id + ") · spec_feature_elaborator"
		assert.Equal(t, 1, strings.Count(out, label+" (1s)"),
			"each fanout item must produce exactly one SUCCESS line, not collapse to a shared spinner")
	}
}

func TestCLISinkSurfacesInterruptedItemsOnClose(t *testing.T) {
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)

	now := time.Now()
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "started", Timestamp: now})
	// No completed/error event before Close — the operator should
	// see this as an interruption rather than silently swallowed.
	s.Close()

	out := buf.String()
	assert.Contains(t, out, "INTERRUPT",
		"items in flight at Close should surface so a SIGINT'd run doesn't lose the in-flight detail")
	assert.Contains(t, out, "survey/spec_scout")
}
