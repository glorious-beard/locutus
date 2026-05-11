package prereqs

import (
	"context"
	"errors"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSpecFixture(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/agents", 0o755))

	// Seed the spec_summarizer agent in .borg/agents so scaffold.LoadAgent
	// can find it. The fields are deliberately minimal — the workflow
	// path is exercised end-to-end via the mock executor.
	agentMD := []byte(`---
id: spec_summarizer
role: summarization
models:
  - {provider: anthropic, tier: fast}
output_schema: SpecSummaryResult
---
# Identity

Summarize one spec node.
`)
	require.NoError(t, fs.WriteFile(".borg/agents/spec_summarizer.md", agentMD, 0o644))
	return fs
}

func TestEnsureSpecsContainSummaries_Greenfield(t *testing.T) {
	fs := setupSpecFixture(t)
	err := EnsureSpecsContainSummaries(context.Background(), SummariesContext{FSys: fs}, false)
	assert.NoError(t, err, "no specs => trivially satisfied")
}

func TestEnsureSpecsContainSummaries_AllPresentNoRegen(t *testing.T) {
	fs := setupSpecFixture(t)

	dec := spec.Decision{ID: "dec-a", Title: "A", Summary: "Decision A summary.", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-a", dec, "body"))

	feat := spec.Feature{ID: "feat-a", Title: "F", Summary: "Feature A summary.", Status: spec.FeatureStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-a", feat, "body"))

	err := EnsureSpecsContainSummaries(context.Background(), SummariesContext{FSys: fs}, false)
	assert.NoError(t, err, "all summaries present => assertion passes without regen")
}

func TestEnsureSpecsContainSummaries_MissingNoRegenFails(t *testing.T) {
	fs := setupSpecFixture(t)

	dec := spec.Decision{ID: "dec-a", Title: "A", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-a", dec, "body"))

	err := EnsureSpecsContainSummaries(context.Background(), SummariesContext{FSys: fs}, false)
	require.Error(t, err)

	var sErr *SummariesError
	require.True(t, errors.As(err, &sErr))
	assert.Equal(t, []string{"dec-a"}, sErr.Missing)
	assert.Contains(t, err.Error(), "1 spec nodes missing Summary")
	assert.Contains(t, err.Error(), "--check-pre-reqs")
}

func TestEnsureSpecsContainSummaries_RegenFillsViaWorkflow(t *testing.T) {
	fs := setupSpecFixture(t)

	dec := spec.Decision{ID: "dec-x", Title: "X", Status: spec.DecisionStatusActive,
		Rationale: "we picked X over Y because of reason Z."}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-x", dec, "body"))

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_summarizer", Response: &agent.AgentOutput{
			Content: `{"summary":"Adopt X for the workload."}`,
		}},
	)
	sctx := SummariesContext{
		FSys:       fs,
		Executor:   mock,
		Dispatcher: agent.NewDispatcher(mock),
	}

	err := EnsureSpecsContainSummaries(context.Background(), sctx, true)
	require.NoError(t, err)

	loaded, _, err := specio.LoadPair[spec.Decision](fs, ".borg/spec/decisions/dec-x")
	require.NoError(t, err)
	assert.Equal(t, "Adopt X for the workload.", loaded.Summary)
}

func TestEnsureSpecsContainSummaries_RegenReportsFailures(t *testing.T) {
	fs := setupSpecFixture(t)

	dec := spec.Decision{ID: "dec-y", Title: "Y", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-y", dec, "body"))

	// Agent returns empty summary -> workflow records failure.
	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_summarizer", Response: &agent.AgentOutput{
			Content: `{"summary":""}`,
		}},
	)
	sctx := SummariesContext{
		FSys:       fs,
		Executor:   mock,
		Dispatcher: agent.NewDispatcher(mock),
	}

	err := EnsureSpecsContainSummaries(context.Background(), sctx, true)
	require.Error(t, err)

	var sErr *SummariesError
	require.True(t, errors.As(err, &sErr))
	require.Contains(t, sErr.Failed, "dec-y")
}

func TestEnsureSpecsContainSummaries_RegenNeedsDispatcher(t *testing.T) {
	fs := setupSpecFixture(t)
	dec := spec.Decision{ID: "dec-z", Title: "Z", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-z", dec, "body"))

	err := EnsureSpecsContainSummaries(context.Background(), SummariesContext{FSys: fs}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Executor and Dispatcher")
}

func TestDiscoverMissingSummaries_MixedPopulation(t *testing.T) {
	fs := setupSpecFixture(t)

	decWith := spec.Decision{ID: "dec-with", Summary: "Has a summary.", Status: spec.DecisionStatusActive}
	decWithout := spec.Decision{ID: "dec-without", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-with", decWith, "body"))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-without", decWithout, "body"))

	app := spec.Approach{ID: "app-no", Title: "A", ParentID: "feat-x"}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-no.md", app, "body"))

	missing, err := discoverMissingSummaries(fs)
	require.NoError(t, err)

	ids := []string{}
	for _, n := range missing {
		ids = append(ids, n.ID)
	}
	assert.ElementsMatch(t, []string{"dec-without", "app-no"}, ids)
}
