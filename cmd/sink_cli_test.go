package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
)

// newCLISinkForTest constructs a cliSink whose pterm output goes to
// the supplied writer instead of stderr — keeps test output clean and
// makes spinner state assertions deterministic.
func newCLISinkForTest(w *bytes.Buffer) *cliSink {
	mp := pterm.DefaultMultiPrinter.WithWriter(w)
	mp, _ = mp.Start()
	return &cliSink{
		multi:    mp,
		spinners: map[string]*pterm.SpinnerPrinter{},
		starts:   map[string]time.Time{},
		started:  true,
	}
}

func TestCLISinkIgnoresWorkflowLevelEvents(t *testing.T) {
	// Regression: workflow.go emits a lone "started" iteration
	// marker with empty StepID/AgentID, and DAG step events with
	// empty AgentID. The first orphans the spinner (Close used to
	// mark it as "interrupted"); the second produces a redundant
	// duplicate. cliSink should ignore both.
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)
	t.Cleanup(func() { s.Close() })

	now := time.Now()
	// Workflow iteration marker — no stepID, no agentID.
	s.OnEvent(agent.WorkflowEvent{Status: "started", Timestamp: now, Message: "iteration 1/1"})
	// DAG step lifecycle — stepID set, no agentID.
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", Status: "started", Timestamp: now})
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", Status: "completed", Timestamp: now})

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.spinners,
		"workflow-level events without an AgentID must not create per-agent spinners")
}

func TestCLISinkRendersAgentLifecycle(t *testing.T) {
	var buf bytes.Buffer
	s := newCLISinkForTest(&buf)
	t.Cleanup(func() { s.Close() })

	now := time.Now()
	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "started", Timestamp: now})

	s.mu.Lock()
	_, has := s.spinners["survey/spec_scout"]
	s.mu.Unlock()
	assert.True(t, has, "agent-level started should create a keyed spinner")

	s.OnEvent(agent.WorkflowEvent{StepID: "survey", AgentID: "spec_scout", Status: "completed", Timestamp: now.Add(time.Second)})
}
