package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adversarialFallbackAgentDefs returns the AgentDef map every
// adversarial-fallback / per-decision call needs. Tests instantiate
// the executor with this map; the workflows only use the dispatcher
// for actual LLM calls (RunItem path), but the executor still requires
// the map to be populated for any non-RunItem paths it might hit.
func adversarialAgentDefs() map[string]AgentDef {
	return map[string]AgentDef{
		"spec_advocate":       {ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		"spec_challenger":     {ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		"justify_researcher":  {ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		"justify_splitter":    {ID: "justify_splitter", OutputSchema: "ChallengeSplit"},
		"justify_synthesizer": {ID: "justify_synthesizer", OutputSchema: "SynthesisVerdict"},
	}
}

// TestJustifyAdversarialFanoutWorkflow_PhaseOrdering — the workflow
// fires classify → per_decision → synthesize in order. Asserts on the
// MockExecutor call sequence (tagged by AgentID), proving phase
// ordering is enforced by DependsOn declarations.
func TestJustifyAdversarialFanoutWorkflow_PhaseOrdering(t *testing.T) {
	splitterPayload := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-a", Shard: "shard a"},
		},
		Rationale: "single decision routed",
	}
	challengePayload := ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the spec doesn't address authentication boundaries clearly",
			Evidence:        "GOALS.md line 14 calls out auth as a top-level requirement",
			Counterproposal: "introduce a dedicated auth strategy with per-role boundaries",
		}},
	}
	researchPayload := ResearchBrief{Findings: []Finding{{Query: "q", Result: "r"}}}
	defensePayload := AdversarialDefense{
		JustificationBrief: JustificationBrief{Defense: "Held up on operational grounds."},
		Verdict:            "held_up",
	}
	synthesisPayload := SynthesisVerdict{
		Defense:   "Strategy holds; the only contested decision survived.",
		Verdict:   "held_up",
		Rationale: "single-decision challenge resolved cleanly",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "justify_splitter", Response: &AgentOutput{Content: marshalJSON(t, splitterPayload)}},
		MockResponse{AgentID: "spec_challenger", Response: &AgentOutput{Content: marshalJSON(t, challengePayload)}},
		MockResponse{AgentID: "justify_researcher", Response: &AgentOutput{Content: marshalJSON(t, researchPayload)}},
		MockResponse{AgentID: "spec_advocate", Response: &AgentOutput{Content: marshalJSON(t, defensePayload)}},
		MockResponse{AgentID: "justify_synthesizer", Response: &AgentOutput{Content: marshalJSON(t, synthesisPayload)}},
	)

	state := JustifyState{
		NodeID:              "strat-x",
		NodeMarkdown:        "# strat-x\n\nbody",
		ParentNodeMD:        "# `strat-x` — Test Strategy",
		ParentBody:          "body prose",
		Challenge:           "shard a",
		AdvocateDef:         AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef:       AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef:       AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		SplitterDef:         AgentDef{ID: "justify_splitter", OutputSchema: "ChallengeSplit"},
		SynthesizerDef:      AgentDef{ID: "justify_synthesizer", OutputSchema: "SynthesisVerdict"},
		DecisionRefs:        []SplitterDecisionRef{{ID: "dec-a", Title: "A"}},
		PerDecisionMarkdown: map[string]string{"dec-a": "# dec-a\n\nA body"},
		Dispatcher:          NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFanoutWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	calls := mock.Calls()
	require.Len(t, calls, 5, "fanout workflow fires 5 calls: splitter + challenger + researcher + advocate + synthesizer")
	assert.Equal(t, "justify_splitter", calls[0].Def.ID, "classify must run first")
	assert.Equal(t, "spec_challenger", calls[1].Def.ID, "per_decision starts with challenger")
	assert.Equal(t, "justify_researcher", calls[2].Def.ID, "per_decision researcher follows challenger")
	assert.Equal(t, "spec_advocate", calls[3].Def.ID, "per_decision advocate follows researcher")
	assert.Equal(t, "justify_synthesizer", calls[4].Def.ID, "synthesize must run last")

	require.NotNil(t, state.Split, "classify merge must populate Split")
	require.Len(t, state.PerDecisionResults, 1, "per_decision merge must populate one result per non-empty shard")
	require.NotNil(t, state.Synthesis, "synthesize merge must populate Synthesis")
	assert.Equal(t, "held_up", state.Synthesis.Verdict)
}

// TestJustifyAdversarialFanoutWorkflow_SkipsEmptyShards — splitter
// emits one empty + one non-empty shard. The fanout filter drops the
// empty one so per_decision fires only once.
func TestJustifyAdversarialFanoutWorkflow_SkipsEmptyShards(t *testing.T) {
	splitterPayload := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-a", Shard: ""},                // empty: skip
			{DecisionID: "dec-b", Shard: "real shard text"}, // present: dispatch
		},
		Rationale: "only dec-b is engaged",
	}
	challengePayload := ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the design assumes synchronous writes and that's a real risk for the audit trail",
			Evidence:        "GOALS §7 mandates an immutable audit trail and the current write path is fire-and-forget",
			Counterproposal: "use a write-ahead-log per request with a downstream consumer that emits the audit record",
		}},
	}
	researchPayload := ResearchBrief{Findings: []Finding{{Query: "q", Result: "r"}}}
	defensePayload := AdversarialDefense{
		JustificationBrief: JustificationBrief{Defense: "Defense paragraph."},
		Verdict:            "partially_held_up",
	}
	synthesisPayload := SynthesisVerdict{
		Defense: "Strategy partially holds.",
		Verdict: "partially_held_up",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "justify_splitter", Response: &AgentOutput{Content: marshalJSON(t, splitterPayload)}},
		MockResponse{AgentID: "spec_challenger", Response: &AgentOutput{Content: marshalJSON(t, challengePayload)}},
		MockResponse{AgentID: "justify_researcher", Response: &AgentOutput{Content: marshalJSON(t, researchPayload)}},
		MockResponse{AgentID: "spec_advocate", Response: &AgentOutput{Content: marshalJSON(t, defensePayload)}},
		MockResponse{AgentID: "justify_synthesizer", Response: &AgentOutput{Content: marshalJSON(t, synthesisPayload)}},
	)

	state := JustifyState{
		NodeID:         "strat-x",
		NodeMarkdown:   "# strat-x",
		ParentBody:     "body prose",
		Challenge:      "challenge text",
		AdvocateDef:    AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef:  AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef:  AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		SplitterDef:    AgentDef{ID: "justify_splitter", OutputSchema: "ChallengeSplit"},
		SynthesizerDef: AgentDef{ID: "justify_synthesizer", OutputSchema: "SynthesisVerdict"},
		DecisionRefs: []SplitterDecisionRef{
			{ID: "dec-a", Title: "A"},
			{ID: "dec-b", Title: "B"},
		},
		PerDecisionMarkdown: map[string]string{
			"dec-a": "# dec-a body",
			"dec-b": "# dec-b body",
		},
		Dispatcher: NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFanoutWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	require.Len(t, state.PerDecisionResults, 1, "empty shard must skip per_decision dispatch")
	assert.Equal(t, "dec-b", state.PerDecisionResults[0].DecisionID,
		"only the non-empty shard's decision should produce a result")

	// Total calls: splitter + 1 challenger + 1 researcher + 1 advocate + synthesizer = 5
	assert.Equal(t, 5, mock.CallCount(),
		"empty shard saves 3 LLM calls (challenger + researcher + advocate for that decision)")
}

// TestJustifyAdversarialFanoutWorkflow_SynthesizeFiresWithProseOnly —
// when every decision shard is empty but parent_prose_shard is
// non-empty, per_decision produces zero results but synthesize still
// fires (the parent-prose shard has signal worth synthesizing).
func TestJustifyAdversarialFanoutWorkflow_SynthesizeFiresWithProseOnly(t *testing.T) {
	splitterPayload := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-a", Shard: ""}, // no per-decision engagement
		},
		ParentProseShard: "the rationale's hiring-velocity claim doesn't hold up",
		Rationale:        "challenge engages parent prose only",
	}
	synthesisPayload := SynthesisVerdict{
		Defense: "Parent prose contested; no per-decision break.",
		Verdict: "partially_held_up",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "justify_splitter", Response: &AgentOutput{Content: marshalJSON(t, splitterPayload)}},
		MockResponse{AgentID: "justify_synthesizer", Response: &AgentOutput{Content: marshalJSON(t, synthesisPayload)}},
	)

	state := JustifyState{
		NodeID:         "strat-x",
		NodeMarkdown:   "# strat-x",
		ParentBody:     "body prose",
		Challenge:      "challenge text",
		AdvocateDef:    AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef:  AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef:  AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		SplitterDef:    AgentDef{ID: "justify_splitter", OutputSchema: "ChallengeSplit"},
		SynthesizerDef: AgentDef{ID: "justify_synthesizer", OutputSchema: "SynthesisVerdict"},
		DecisionRefs:   []SplitterDecisionRef{{ID: "dec-a", Title: "A"}},
		PerDecisionMarkdown: map[string]string{
			"dec-a": "# dec-a body",
		},
		Dispatcher: NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFanoutWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	assert.Empty(t, state.PerDecisionResults, "no per-decision dispatches when every shard is empty")
	require.NotNil(t, state.Synthesis,
		"synthesize must still fire: parent_prose_shard alone is enough signal to synthesize")
	assert.Equal(t, "partially_held_up", state.Synthesis.Verdict)

	// Total calls: splitter + synthesizer = 2 (no per_decision dispatches)
	assert.Equal(t, 2, mock.CallCount(),
		"prose-only path: splitter + synthesizer, no per_decision sub-flows")
}

// TestJustifyAdversarialFanoutWorkflow_SynthesizeSkippedWhenNothingToSynthesize
// — when both per_decision results AND parent_prose_shard are empty,
// the synthesize phase is gated off by the conditional. No
// "nothing to synthesize" error reaches the cmd layer.
func TestJustifyAdversarialFanoutWorkflow_SynthesizeSkippedWhenNothingToSynthesize(t *testing.T) {
	splitterPayload := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-a", Shard: ""},
			{DecisionID: "dec-b", Shard: ""},
		},
		ParentProseShard: "", // also empty
		Rationale:        "challenge doesn't engage this strategy",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "justify_splitter", Response: &AgentOutput{Content: marshalJSON(t, splitterPayload)}},
	)

	state := JustifyState{
		NodeID:         "strat-x",
		NodeMarkdown:   "# strat-x",
		ParentBody:     "body prose",
		Challenge:      "challenge text",
		AdvocateDef:    AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef:  AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef:  AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		SplitterDef:    AgentDef{ID: "justify_splitter", OutputSchema: "ChallengeSplit"},
		SynthesizerDef: AgentDef{ID: "justify_synthesizer", OutputSchema: "SynthesisVerdict"},
		DecisionRefs: []SplitterDecisionRef{
			{ID: "dec-a", Title: "A"},
			{ID: "dec-b", Title: "B"},
		},
		PerDecisionMarkdown: map[string]string{
			"dec-a": "# dec-a body",
			"dec-b": "# dec-b body",
		},
		Dispatcher: NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFanoutWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err, "skipping synthesize on empty inputs is not an error")

	assert.Empty(t, state.PerDecisionResults, "no per-decision results")
	assert.Nil(t, state.Synthesis, "synthesize must NOT fire when both inputs are empty")

	// Only splitter fires; synthesize is gated off.
	assert.Equal(t, 1, mock.CallCount(),
		"only the splitter dispatches; synthesize is conditional on having something to synthesize")
}

// TestHasSynthesisInputs — directly exercises the conditional
// closure across the truth-table cases. The phase ordering tests
// above hit this through the executor; this test pins the predicate
// itself as a unit so failures in the predicate land at this test
// name rather than higher up.
func TestHasSynthesisInputs(t *testing.T) {
	t.Run("nil state returns false", func(t *testing.T) {
		assert.False(t, hasSynthesisInputs(nil))
	})
	t.Run("empty state returns false", func(t *testing.T) {
		assert.False(t, hasSynthesisInputs(&JustifyState{}))
	})
	t.Run("per-decision results alone returns true", func(t *testing.T) {
		s := &JustifyState{
			PerDecisionResults: []FanOutDecisionResult{{DecisionID: "dec-a"}},
		}
		assert.True(t, hasSynthesisInputs(s))
	})
	t.Run("parent prose alone returns true", func(t *testing.T) {
		s := &JustifyState{
			Split: &ChallengeSplit{ParentProseShard: "engaged prose"},
		}
		assert.True(t, hasSynthesisInputs(s))
	})
	t.Run("both empty (split present, no shards or prose) returns false", func(t *testing.T) {
		s := &JustifyState{
			Split: &ChallengeSplit{},
		}
		assert.False(t, hasSynthesisInputs(s))
	})
	t.Run("whitespace-only prose returns false", func(t *testing.T) {
		s := &JustifyState{
			Split: &ChallengeSplit{ParentProseShard: "   \n\t  "},
		}
		assert.False(t, hasSynthesisInputs(s))
	})
}

// TestFanoutJustifyDecisions_FilterEmptyShards — the fanout function
// filters empty shards out so the executor never dispatches a wasted
// per-decision sub-flow.
func TestFanoutJustifyDecisions_FilterEmptyShards(t *testing.T) {
	state := &JustifyState{
		Split: &ChallengeSplit{
			DecisionShards: []DecisionShard{
				{DecisionID: "dec-a", Shard: "real text"},
				{DecisionID: "dec-b", Shard: ""},       // skip
				{DecisionID: "dec-c", Shard: "  \t  "}, // whitespace only: skip
				{DecisionID: "dec-d", Shard: "more real text"},
			},
		},
		DecisionRefs: []SplitterDecisionRef{
			{ID: "dec-a", Title: "A"},
			{ID: "dec-b", Title: "B"},
			{ID: "dec-c", Title: "C"},
			{ID: "dec-d", Title: "D"},
		},
		PerDecisionMarkdown: map[string]string{
			"dec-a": "A md",
			"dec-b": "B md",
			"dec-c": "C md",
			"dec-d": "D md",
		},
	}

	items, err := fanoutJustifyDecisions(state)
	require.NoError(t, err)
	require.Len(t, items, 2, "only non-empty shards should produce fanout items")

	// Item content threads decision id, title, markdown, and shard.
	for _, raw := range items {
		assert.Contains(t, raw, "node_markdown", "fanout item must carry per-decision NodeMarkdown")
		assert.Contains(t, raw, "shard", "fanout item must carry the shard text")
	}
}

// TestFanoutJustifyDecisions_NilSplit — defensive: the fanout closure
// returns nil + no error when classify hasn't merged a split (shouldn't
// happen at runtime since DependsOn forces ordering, but worth pinning).
func TestFanoutJustifyDecisions_NilSplit(t *testing.T) {
	items, err := fanoutJustifyDecisions(&JustifyState{})
	require.NoError(t, err)
	assert.Empty(t, items)

	items, err = fanoutJustifyDecisions(nil)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestJustifySoloWorkflow_SingleStep — solo path runs exactly one
// LLM call (advocate) and produces a JustificationBrief on state.
func TestJustifySoloWorkflow_SingleStep(t *testing.T) {
	briefPayload := JustificationBrief{
		Defense:                     "This holds up.",
		GoalClausesCited:            []string{"GOALS §1"},
		ConditionsUnderWhichInvalid: []string{"if X changes"},
	}
	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_advocate", Response: &AgentOutput{Content: marshalJSON(t, briefPayload)}},
	)

	state := JustifyState{
		NodeID:       "dec-x",
		NodeMarkdown: "# dec-x\n\nbody",
		AdvocateDef:  AgentDef{ID: "spec_advocate", OutputSchema: "JustificationBrief"},
		Dispatcher:   NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor: mock,
		AgentDefs: map[string]AgentDef{
			"spec_advocate": {ID: "spec_advocate", OutputSchema: "JustificationBrief"},
		},
		Workflow: JustifySoloWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	require.NotNil(t, state.Brief, "solo workflow must populate Brief")
	assert.Equal(t, "This holds up.", state.Brief.Defense)
	assert.Equal(t, 1, mock.CallCount(), "solo workflow fires exactly one LLM call")

	// Solo path should NOT populate adversarial outputs.
	assert.Nil(t, state.AdversarialDefense, "solo path leaves AdversarialDefense empty")
	assert.Nil(t, state.ChallengerOut, "solo path leaves ChallengerOut empty")
}

// TestJustifyAdversarialFallbackWorkflow_PhaseOrdering — sequential
// challenge → research → defend, three calls total. Fallback workflow
// for parents without decisions and direct decision targets.
func TestJustifyAdversarialFallbackWorkflow_PhaseOrdering(t *testing.T) {
	challengePayload := ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the chosen path bakes in vendor lock-in via proprietary APIs",
			Evidence:        "GOALS §4 calls out cost discipline; switching costs from a sole-source vendor erode that posture",
			Counterproposal: "self-host the equivalent OSS alternative or adopt a vendor with documented data export",
		}},
	}
	researchPayload := ResearchBrief{Findings: []Finding{{Query: "q", Result: "r"}}}
	defensePayload := AdversarialDefense{
		JustificationBrief: JustificationBrief{Defense: "Defense"},
		Verdict:            "held_up",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_challenger", Response: &AgentOutput{Content: marshalJSON(t, challengePayload)}},
		MockResponse{AgentID: "justify_researcher", Response: &AgentOutput{Content: marshalJSON(t, researchPayload)}},
		MockResponse{AgentID: "spec_advocate", Response: &AgentOutput{Content: marshalJSON(t, defensePayload)}},
	)

	state := JustifyState{
		NodeID:        "dec-x",
		NodeMarkdown:  "# dec-x\n\nbody",
		Challenge:     "why",
		AdvocateDef:   AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef: AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef: AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		Dispatcher:    NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFallbackWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	calls := mock.Calls()
	require.Len(t, calls, 3, "adversarial fallback fires exactly 3 calls")
	assert.Equal(t, "spec_challenger", calls[0].Def.ID, "challenge runs first")
	assert.Equal(t, "justify_researcher", calls[1].Def.ID, "research runs second")
	assert.Equal(t, "spec_advocate", calls[2].Def.ID, "defend runs last")

	require.NotNil(t, state.ChallengerOut)
	require.NotNil(t, state.ResearcherOut)
	require.NotNil(t, state.AdversarialDefense)
	assert.Equal(t, "held_up", state.AdversarialDefense.Verdict)
}

// TestJustifyAdversarialFallbackWorkflow_PreservesToolOutcomes — the
// research step's ToolOutcomes (post-call metadata, not part of the
// model's JSON schema) survive the workflow's serialize/deserialize
// hop via researchEnvelope. The advocate's prompt builder reads
// ResearcherOut.FailedQueries() to render the ungrounded-queries
// section; losing ToolOutcomes here would silently regress that
// warning surface.
func TestJustifyAdversarialFallbackWorkflow_PreservesToolOutcomes(t *testing.T) {
	challengePayload := ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the chosen path bakes in vendor lock-in via proprietary APIs",
			Evidence:        "GOALS §4 calls out cost discipline; switching costs from a sole-source vendor erode that posture",
			Counterproposal: "self-host the equivalent OSS alternative or adopt a vendor with documented data export",
		}},
	}
	researchPayload := ResearchBrief{Findings: []Finding{{Query: "q", Result: "r"}}}
	// MockResponse with grounded tool calls including a failed one.
	researchResp := &AgentOutput{
		Content: marshalJSON(t, researchPayload),
		ToolCalls: []ToolCall{
			{Name: "web_search", Query: "q1", Status: "success"},
			{Name: "web_search", Query: "q2", Status: "error", ErrorCode: "unavailable"},
		},
	}
	defensePayload := AdversarialDefense{
		JustificationBrief: JustificationBrief{Defense: "Defense"},
		Verdict:            "held_up",
	}

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_challenger", Response: &AgentOutput{Content: marshalJSON(t, challengePayload)}},
		MockResponse{AgentID: "justify_researcher", Response: researchResp},
		MockResponse{AgentID: "spec_advocate", Response: &AgentOutput{Content: marshalJSON(t, defensePayload)}},
	)

	state := JustifyState{
		NodeID:        "dec-x",
		NodeMarkdown:  "# dec-x",
		Challenge:     "why",
		AdvocateDef:   AgentDef{ID: "spec_advocate", OutputSchema: "AdversarialDefense"},
		ChallengerDef: AgentDef{ID: "spec_challenger", OutputSchema: "ChallengeBrief"},
		ResearcherDef: AgentDef{ID: "justify_researcher", OutputSchema: "ResearchBrief"},
		Dispatcher:    NewDispatcher(mock),
	}

	exec := &WorkflowExecutor[JustifyState]{
		Executor:  mock,
		AgentDefs: adversarialAgentDefs(),
		Workflow:  JustifyAdversarialFallbackWorkflow,
	}

	_, err := exec.Run(context.Background(), &state)
	require.NoError(t, err)

	require.NotNil(t, state.ResearcherOut)
	require.Len(t, state.ResearcherOut.ToolOutcomes, 2,
		"workflow must preserve ToolOutcomes across the merge boundary")
	failed := state.ResearcherOut.FailedQueries()
	require.Len(t, failed, 1, "failed-query subset must survive serialize/deserialize")
	assert.Equal(t, "q2", failed[0].Query)
	assert.Equal(t, "unavailable", failed[0].ErrorCode)

	// Belt-and-suspenders: the advocate's user prompt (built inside
	// the defend step) must include the ungrounded-queries section
	// when ToolOutcomes carries failures. Verify by reading the
	// recorded user message off the advocate call.
	calls := mock.Calls()
	require.Len(t, calls, 3)
	advocateUser := calls[2].Input.Messages[0].Content
	assert.True(t, strings.Contains(advocateUser, "Ungrounded research queries"),
		"advocate prompt must surface the ungrounded section when ToolOutcomes carries failures")
}

// TestSnapshotJustifyState_DeepCopiesSlicesAndMap — the snapshot
// closure produces a value-copy whose slices and map are independent
// of the source. Forward-looking guard against parallel-fanout
// mutations: today nothing flips Parallel:true on the per_decision
// step, but if a future change does, the snapshot must already
// isolate parallel agents from in-flight appends.
func TestSnapshotJustifyState_DeepCopiesSlicesAndMap(t *testing.T) {
	src := &JustifyState{
		DecisionRefs: []SplitterDecisionRef{
			{ID: "dec-a", Title: "A"},
			{ID: "dec-b", Title: "B"},
		},
		PerDecisionResults: []FanOutDecisionResult{
			{DecisionID: "dec-a", DecisionTitle: "A"},
		},
		PerDecisionMarkdown: map[string]string{
			"dec-a": "A md",
		},
	}

	snap := snapshotJustifyState(src)

	// Mutate src; snap must remain unchanged.
	src.DecisionRefs[0].Title = "MUTATED"
	src.PerDecisionResults = append(src.PerDecisionResults, FanOutDecisionResult{DecisionID: "dec-x"})
	src.PerDecisionMarkdown["dec-a"] = "MUTATED"

	assert.Equal(t, "A", snap.DecisionRefs[0].Title,
		"snapshot's DecisionRefs must be independent of source mutation")
	assert.Len(t, snap.PerDecisionResults, 1,
		"snapshot's PerDecisionResults must not see the post-snapshot append")
	assert.Equal(t, "A md", snap.PerDecisionMarkdown["dec-a"],
		"snapshot's PerDecisionMarkdown must be independent of source mutation")
}
