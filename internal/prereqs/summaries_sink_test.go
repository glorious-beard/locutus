package prereqs

import (
	"context"
	"sync"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSink collects every WorkflowEvent it receives. Lets tests
// inspect what the prereq workflow emitted without depending on the
// CLI's pterm rendering layer.
type captureSink struct {
	mu     sync.Mutex
	events []agent.WorkflowEvent
}

func (c *captureSink) OnEvent(e agent.WorkflowEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureSink) Close() {}

func (c *captureSink) statuses() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e.Status)
	}
	return out
}

// TestEnsureSpecsContainSummaries_EmitsEventsToSink verifies the
// regen=true path threads the workflow's per-call lifecycle events
// through the configured sink. This is the wiring the CLI relies on
// to render spinner state for the prereq pass; if it breaks, the
// operator gets no console feedback on a long retrofit.
func TestEnsureSpecsContainSummaries_EmitsEventsToSink(t *testing.T) {
	fs := setupSpecFixture(t)

	dec := spec.Decision{
		ID:        "dec-x",
		Title:     "X",
		Status:    spec.DecisionStatusActive,
		Rationale: "we picked X over Y because of reason Z.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-x", dec, "body"))

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_summarizer", Response: &agent.AgentOutput{
			Content: `{"summary":"Adopt X for the workload."}`,
		}},
	)
	sink := &captureSink{}
	sctx := SummariesContext{
		FSys:       fs,
		Executor:   mock,
		Dispatcher: agent.NewDispatcher(mock),
		Sink:       sink,
	}

	err := EnsureSpecsContainSummaries(context.Background(), sctx, true)
	require.NoError(t, err)

	statuses := sink.statuses()
	// Workflow's RunItem path emits queued → started → completed for
	// each fanout slot. Exactly one slot here (one missing summary),
	// so the sequence must include all three.
	assert.Contains(t, statuses, "queued", "queued event must reach the sink")
	assert.Contains(t, statuses, "started", "started event must reach the sink")
	assert.Contains(t, statuses, "completed", "completed event must reach the sink")
}

// TestEnsureSpecsContainSummaries_NilSinkIsSilent confirms a nil sink
// is the silent fallback rather than a panic. Important because
// non-CLI callers (tests, MCP) may not have a sink available.
func TestEnsureSpecsContainSummaries_NilSinkIsSilent(t *testing.T) {
	fs := setupSpecFixture(t)
	dec := spec.Decision{ID: "dec-y", Title: "Y", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-y", dec, "body"))

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_summarizer", Response: &agent.AgentOutput{
			Content: `{"summary":"Use Y."}`,
		}},
	)
	sctx := SummariesContext{
		FSys:       fs,
		Executor:   mock,
		Dispatcher: agent.NewDispatcher(mock),
		Sink:       nil, // explicit nil
	}

	err := EnsureSpecsContainSummaries(context.Background(), sctx, true)
	assert.NoError(t, err)
}
