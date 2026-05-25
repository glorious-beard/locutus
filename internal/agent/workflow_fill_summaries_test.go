package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFillSummariesWorkflow_WritesBackToJSONNodes(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	dec := spec.Decision{
		ID: "dec-postgres", Title: "Postgres for OLTP",
		Status: spec.DecisionStatusActive, Confidence: 0.9,
		Rationale: "We need mature replication and time-tested operational tooling.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-postgres", dec, "decision body"))

	feat := spec.Feature{
		ID: "feat-dashboard", Title: "Dashboard",
		Status:      spec.FeatureStatusActive,
		Description: "Operators see their fleet at a glance.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-dashboard", feat, "feature body"))

	decContent, _ := fs.ReadFile(".borg/spec/decisions/dec-postgres.json")
	featContent, _ := fs.ReadFile(".borg/spec/features/feat-dashboard.json")

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec-summarizer", Response: &AgentOutput{
			Content: marshalJSON(t, SpecSummaryResult{Summary: "Adopt Postgres for the OLTP store."}),
		}},
		MockResponse{AgentID: "spec-summarizer", Response: &AgentOutput{
			Content: marshalJSON(t, SpecSummaryResult{Summary: "Operators view fleet status from a single dashboard."}),
		}},
	)

	state := FillSummariesState{
		FSys:          fs,
		SummarizerDef: AgentDef{ID: "spec-summarizer", OutputSchema: "SpecSummaryResult"},
		Dispatcher:    NewDispatcher(mock),
		Missing: []MissingSummaryNode{
			{Kind: "decision", ID: "dec-postgres", Path: ".borg/spec/decisions/dec-postgres", Content: string(decContent)},
			{Kind: "feature", ID: "feat-dashboard", Path: ".borg/spec/features/feat-dashboard", Content: string(featContent)},
		},
	}

	exec := &WorkflowExecutor[FillSummariesState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"spec-summarizer": {ID: "spec-summarizer"}},
		Workflow:  FillSummariesWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"dec-postgres", "feat-dashboard"}, state.Filled)
	assert.Empty(t, state.Failed)

	// Fanout is parallel; the mock's FIFO queue may route either
	// response to either node. Assert each node got a non-empty
	// Summary drawn from the response set rather than pinning which
	// response went where.
	allowed := map[string]bool{
		"Adopt Postgres for the OLTP store.":                       true,
		"Operators view fleet status from a single dashboard.":     true,
	}

	gotDec, _, err := specio.LoadPair[spec.Decision](fs, ".borg/spec/decisions/dec-postgres")
	require.NoError(t, err)
	assert.True(t, allowed[gotDec.Summary], "decision summary should be one of the scripted responses, got %q", gotDec.Summary)

	gotFeat, _, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-dashboard")
	require.NoError(t, err)
	assert.True(t, allowed[gotFeat.Summary], "feature summary should be one of the scripted responses, got %q", gotFeat.Summary)

	assert.NotEqual(t, gotDec.Summary, gotFeat.Summary, "each node should get a distinct scripted response")
}

func TestFillSummariesWorkflow_WritesBackToApproach(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))

	app := spec.Approach{
		ID: "app-fetch", Title: "Fetch endpoint", ParentID: "feat-dashboard",
		Body:      "Build /api/dashboards.",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-fetch.md", app, "Build /api/dashboards."))

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec-summarizer", Response: &AgentOutput{
			Content: marshalJSON(t, SpecSummaryResult{Summary: "Implement the /api/dashboards GET endpoint."}),
		}},
	)

	state := FillSummariesState{
		FSys:          fs,
		SummarizerDef: AgentDef{ID: "spec-summarizer", OutputSchema: "SpecSummaryResult"},
		Dispatcher:    NewDispatcher(mock),
		Missing: []MissingSummaryNode{
			{Kind: "approach", ID: "app-fetch", Path: ".borg/spec/approaches/app-fetch.md", Content: "Build /api/dashboards."},
		},
	}

	exec := &WorkflowExecutor[FillSummariesState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"spec-summarizer": {ID: "spec-summarizer"}},
		Workflow:  FillSummariesWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	assert.Equal(t, []string{"app-fetch"}, state.Filled)
	assert.Empty(t, state.Failed)

	loaded, body, err := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-fetch.md")
	require.NoError(t, err)
	assert.Equal(t, "Implement the /api/dashboards GET endpoint.", loaded.Summary)
	assert.Equal(t, "Build /api/dashboards.", body, "body preserved verbatim")
}

func TestFillSummariesWorkflow_EmptyMissingShortCircuits(t *testing.T) {
	fs := specio.NewMemFS()
	mock := NewMockExecutor()

	state := FillSummariesState{
		FSys:          fs,
		SummarizerDef: AgentDef{ID: "spec-summarizer"},
		Dispatcher:    NewDispatcher(mock),
		Missing:       nil,
	}

	exec := &WorkflowExecutor[FillSummariesState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"spec-summarizer": {ID: "spec-summarizer"}},
		Workflow:  FillSummariesWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)
	assert.Empty(t, state.Filled)
	assert.Empty(t, state.Failed)
	assert.Empty(t, mock.Calls(), "no items => no dispatches")
}

func TestFillSummariesWorkflow_RecordsFailureOnEmptySummary(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	dec := spec.Decision{ID: "dec-x", Title: "X", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-x", dec, "body"))

	// Agent returns empty summary -> runSummarizeOne records a failure.
	mock := NewMockExecutor(
		MockResponse{AgentID: "spec-summarizer", Response: &AgentOutput{
			Content: marshalJSON(t, SpecSummaryResult{Summary: ""}),
		}},
	)

	state := FillSummariesState{
		FSys:          fs,
		SummarizerDef: AgentDef{ID: "spec-summarizer", OutputSchema: "SpecSummaryResult"},
		Dispatcher:    NewDispatcher(mock),
		Missing: []MissingSummaryNode{
			{Kind: "decision", ID: "dec-x", Path: ".borg/spec/decisions/dec-x", Content: "{}"},
		},
	}

	exec := &WorkflowExecutor[FillSummariesState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"spec-summarizer": {ID: "spec-summarizer"}},
		Workflow:  FillSummariesWorkflow,
	}

	_, _ = exec.Run(context.Background(), &state)

	assert.Empty(t, state.Filled, "empty summary should not be recorded as filled")
	require.Contains(t, state.Failed, "dec-x")
	assert.Contains(t, state.Failed["dec-x"].Error(), "dec-x")
}

func TestFanoutMissingSummariesShape(t *testing.T) {
	state := FillSummariesState{
		Missing: []MissingSummaryNode{
			{Kind: "feature", ID: "feat-a", Path: ".borg/spec/features/feat-a", Content: "{}"},
			{Kind: "strategy", ID: "strat-b", Path: ".borg/spec/strategies/strat-b", Content: "{}"},
		},
	}

	items, err := fanoutMissingSummaries(&state)
	require.NoError(t, err)
	require.Len(t, items, 2)

	var got []MissingSummaryNode
	for _, raw := range items {
		var n MissingSummaryNode
		require.NoError(t, json.Unmarshal([]byte(raw), &n))
		got = append(got, n)
	}
	ids := []string{got[0].ID, got[1].ID}
	assert.ElementsMatch(t, []string{"feat-a", "strat-b"}, ids)
}
