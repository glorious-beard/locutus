package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractFanoutItems — outline.features and outline.strategies
// resolve to one raw-JSON string per element, parseable back to the
// original typed shape.
func TestExtractFanoutItems(t *testing.T) {
	outline := Outline{
		Features: []OutlineFeature{
			{ID: "feat-a", Title: "A", Summary: "first"},
			{ID: "feat-b", Title: "B", Summary: "second"},
		},
		Strategies: []OutlineStrategy{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Summary: "stack choice"},
		},
	}
	raw, err := json.Marshal(outline)
	require.NoError(t, err)
	state := &PlanningState{Outline: string(raw)}

	t.Run("features path returns one item per feature", func(t *testing.T) {
		items, err := extractFanoutItems(state, "outline.features")
		require.NoError(t, err)
		require.Len(t, items, 2)

		var first OutlineFeature
		require.NoError(t, json.Unmarshal([]byte(items[0]), &first))
		assert.Equal(t, "feat-a", first.ID)
		assert.Equal(t, "first", first.Summary)
	})

	t.Run("strategies path returns one item per strategy", func(t *testing.T) {
		items, err := extractFanoutItems(state, "outline.strategies")
		require.NoError(t, err)
		require.Len(t, items, 1)

		var first OutlineStrategy
		require.NoError(t, json.Unmarshal([]byte(items[0]), &first))
		assert.Equal(t, "strat-x", first.ID)
		assert.Equal(t, "foundational", first.Kind)
	})

	t.Run("unknown path errors", func(t *testing.T) {
		_, err := extractFanoutItems(state, "outline.bogus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported fanout path")
	})

	t.Run("missing outline returns empty", func(t *testing.T) {
		empty := &PlanningState{}
		items, err := extractFanoutItems(empty, "outline.features")
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}

// TestExecuteRoundFanoutSpawnsOnePerItem — the executor spawns one
// agent invocation per fanout element, threading FanoutItem through
// to the agent's snapshot. The mock LLM's call count proves N items
// produce N calls; inspecting the per-call user message proves each
// call saw a different FanoutItem.
func TestExecuteRoundFanoutSpawnsOnePerItem(t *testing.T) {
	outline := Outline{
		Features: []OutlineFeature{
			{ID: "feat-a", Title: "A", Summary: "first"},
			{ID: "feat-b", Title: "B", Summary: "second"},
			{ID: "feat-c", Title: "C", Summary: "third"},
		},
	}
	raw, _ := json.Marshal(outline)

	state := &PlanningState{
		Prompt:  "Build it.",
		Outline: string(raw),
	}

	defs := map[string]AgentDef{
		"spec_feature_elaborator": {
			ID:           "spec_feature_elaborator",
			Role:         "planning",
			Capability:   CapabilityStrong,
			OutputSchema: "RawFeatureProposal",
			SystemPrompt: "You are an elaborator.",
		},
	}

	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: `{"id":"feat-a","title":"A","decisions":[]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"id":"feat-b","title":"B","decisions":[]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"id":"feat-c","title":"C","decisions":[]}`, Model: "m"}},
	)

	ex := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: defs,
		Workflow:  &Workflow{},
	}
	step := WorkflowStep{
		ID:       "elaborate_features",
		Agent:    "spec_feature_elaborator",
		Parallel: true,
		Fanout:   "outline.features",
		MergeAs:  "elaborated_features",
	}

	results, err := ex.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	require.Len(t, results, 3, "three outline items ⇒ three agent invocations")
	assert.Equal(t, 3, mock.CallCount())

	// Each invocation's user message should contain the per-item id —
	// the projection threads FanoutItem into the prompt as JSON.
	calls := mock.Calls()
	require.Len(t, calls, 3)
	allPrompts := make([]string, 0, 3)
	for _, c := range calls {
		// Last message is the user message.
		msgs := c.Request.Messages
		require.NotEmpty(t, msgs)
		allPrompts = append(allPrompts, msgs[len(msgs)-1].Content)
	}
	// Order may not match outline order under Parallel:true; check
	// each id appears in some prompt.
	for _, want := range []string{"feat-a", "feat-b", "feat-c"} {
		found := false
		for _, p := range allPrompts {
			if assert.ObjectsAreEqual(false, false) && p == "" {
				continue
			}
			if contains(p, want) {
				found = true
				break
			}
		}
		assert.True(t, found, "expected fanout-item id %q in some elaborator prompt", want)
	}
}

// TestExecuteRoundFanoutEmptyOutlineNoOps — the fanout step with
// nothing to iterate returns cleanly without firing any agent calls.
func TestExecuteRoundFanoutEmptyOutlineNoOps(t *testing.T) {
	state := &PlanningState{Outline: `{"features":[],"strategies":[]}`}
	mock := NewMockLLM()
	ex := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"spec_feature_elaborator": {ID: "spec_feature_elaborator"}},
	}
	step := WorkflowStep{
		ID: "elaborate_features", Agent: "spec_feature_elaborator",
		Fanout: "outline.features",
	}
	results, err := ex.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, mock.CallCount(), "no items ⇒ no LLM calls")
}

// TestAssembleRawProposal — once both fanouts merge, RawProposal is a
// valid RawSpecProposal with all features and strategies.
func TestAssembleRawProposal(t *testing.T) {
	state := &PlanningState{
		ElaboratedFeatures: []string{
			`{"id":"feat-a","title":"A","decisions":[{"title":"X"}]}`,
			`{"id":"feat-b","title":"B","decisions":[]}`,
		},
		ElaboratedStrategies: []string{
			`{"id":"strat-x","title":"Stack","kind":"foundational","body":"prose","decisions":[]}`,
		},
	}
	raw, ok := assembleRawProposal(state)
	require.True(t, ok)

	var assembled RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(raw), &assembled))
	require.Len(t, assembled.Features, 2)
	require.Len(t, assembled.Strategies, 1)
	assert.Equal(t, "feat-a", assembled.Features[0].ID)
	assert.Equal(t, "strat-x", assembled.Strategies[0].ID)
}

// TestAssembleRawProposalSkipsMalformed — one bad elaborator output
// shouldn't poison the rest of the assembly. The malformed entry is
// dropped (with a slog warning); the others survive.
func TestAssembleRawProposalSkipsMalformed(t *testing.T) {
	state := &PlanningState{
		ElaboratedFeatures: []string{
			`{"id":"feat-a","title":"A"}`,
			`not json`,
			`{"id":"feat-c","title":"C"}`,
		},
	}
	raw, ok := assembleRawProposal(state)
	require.True(t, ok)
	var assembled RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(raw), &assembled))
	require.Len(t, assembled.Features, 2, "malformed entry dropped; valid entries survive")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOfStr(s, sub) >= 0))
}

func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
