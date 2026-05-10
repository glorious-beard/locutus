package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// JustifyState is the per-verb workflow state for `locutus justify`.
//
// One state value covers all three justify workflows (Solo,
// AdversarialFallback, AdversarialFanout). The cmd layer picks a
// workflow at dispatch time based on (a) whether --against was
// supplied and (b) whether the parent has decisions. Fields a given
// workflow doesn't use stay zero-valued.
//
// Inputs are populated by the cmd layer before the workflow runs.
// Intermediate outputs (Split, PerDecisionResults, Synthesis) are
// stashed by per-step Merge handlers so downstream steps can read
// upstream state. Final outputs (Brief, AdversarialDefense, plus the
// fanout-trio Split + PerDecisionResults + Synthesis) are read back
// by the cmd layer after the workflow returns; the cmd layer
// assembles a FanOutResult from the trio for the renderer.
//
// The Dispatcher field carries the AgentDispatcher each step's
// RunItem closure uses to dispatch the underlying agents. Cleanest
// place for it: any step closure that needs dispatch can pull it off
// the snapshot's State. Alternative was a closure-captured dispatcher
// per step, but that would force the workflow declarations into
// constructor functions; carrying it on state keeps the workflows
// var-declarable like PlanningWorkflow.
type JustifyState struct {
	// Inputs (set by cmd layer before Run).

	NodeID        string
	NodeMarkdown  string
	GoalsBody     string
	Challenge     string
	ChallengerOut *ChallengeBrief
	ResearcherOut *ResearchBrief

	// AgentDefs loaded by the cmd layer from scaffold.LoadAgent. Only
	// the ones the chosen workflow needs must be populated:
	//   Solo: Advocate
	//   AdversarialFallback: Advocate, Challenger, Researcher
	//   AdversarialFanout:   all five
	AdvocateDef    AgentDef
	ChallengerDef  AgentDef
	ResearcherDef  AgentDef
	SplitterDef    AgentDef
	SynthesizerDef AgentDef

	// Fanout inputs.

	ParentBody          string
	ParentNodeMD        string
	PerDecisionMarkdown map[string]string
	DecisionRefs        []SplitterDecisionRef

	// Intermediate outputs (populated by Merge handlers).

	Split              *ChallengeSplit
	PerDecisionResults []FanOutDecisionResult
	Synthesis          *SynthesisVerdict

	// Final outputs (populated by terminal step's Merge or RunItem
	// closure depending on workflow). The cmd layer reads exactly
	// the field appropriate for the workflow it ran.

	Brief              *JustificationBrief
	AdversarialDefense *AdversarialDefense

	// Dispatcher used by every step's RunItem closure. Carried on
	// state because workflows are package-level vars; each step's
	// closure pulls dispatch off the snapshot.
	Dispatcher AgentDispatcher
}

// snapshotJustifyState returns a value-copy of JustifyState safe for
// concurrent reads by parallel agents. Today no justify workflow
// declares Parallel:true on any step (the per_decision fanout is
// sequential, matching the legacy RunJustifyFanOut comment about
// avoiding parallel dispatch of grounded researchers), so the slice
// fields can't actually be observed mid-mutation. The deep copy is
// belt-and-suspenders for a future migration to parallel fanout —
// without it, flipping Parallel:true on per_decision would silently
// race on PerDecisionResults appends.
func snapshotJustifyState(s *JustifyState) JustifyState {
	out := *s
	if len(s.DecisionRefs) > 0 {
		out.DecisionRefs = make([]SplitterDecisionRef, len(s.DecisionRefs))
		copy(out.DecisionRefs, s.DecisionRefs)
	}
	if len(s.PerDecisionResults) > 0 {
		out.PerDecisionResults = make([]FanOutDecisionResult, len(s.PerDecisionResults))
		copy(out.PerDecisionResults, s.PerDecisionResults)
	}
	if len(s.PerDecisionMarkdown) > 0 {
		out.PerDecisionMarkdown = make(map[string]string, len(s.PerDecisionMarkdown))
		for k, v := range s.PerDecisionMarkdown {
			out.PerDecisionMarkdown[k] = v
		}
	}
	return out
}

// JustifySoloWorkflow runs the spec_advocate alone. Single phase with
// no challenger / researcher. Mirrors the old RunJustify direct-call
// path; the workflowization gives it the same observability shape as
// the other verbs (workflow.phase span, lifecycle events on the sink).
var JustifySoloWorkflow = &Workflow[JustifyState]{
	Snapshot: snapshotJustifyState,
	Rounds: []WorkflowStep[JustifyState]{
		{
			ID:      "defend",
			Agents:  []string{"spec_advocate"},
			RunItem: runJustifySoloDefend,
			Merge:   mergeJustifySoloDefend,
		},
	},
	MaxRounds: 1,
}

// JustifyAdversarialFallbackWorkflow runs the 3-step adversarial
// dialogue against a single target (decision OR a parent without
// referenced decisions). Sequential phases, each calling the existing
// dispatcher-driven helpers via RunItem.
var JustifyAdversarialFallbackWorkflow = &Workflow[JustifyState]{
	Snapshot: snapshotJustifyState,
	Rounds: []WorkflowStep[JustifyState]{
		{
			ID:      "challenge",
			Agents:  []string{"spec_challenger"},
			RunItem: runJustifyChallenge,
			Merge:   mergeJustifyChallenge,
		},
		{
			ID:        "research",
			Agents:    []string{"justify_researcher"},
			DependsOn: []string{"challenge"},
			RunItem:   runJustifyResearch,
			Merge:     mergeJustifyResearch,
		},
		{
			ID:        "defend",
			Agents:    []string{"spec_advocate"},
			DependsOn: []string{"research"},
			RunItem:   runJustifyAdversarialDefend,
			Merge:     mergeJustifyAdversarialDefend,
		},
	},
	MaxRounds: 1,
}

// JustifyAdversarialFanoutWorkflow handles parents that reference
// decisions (strategy / feature / bug / approach). The classify step
// splits the user's challenge across decisions; per_decision fans out
// the existing 3-step adversarial sub-flow per non-empty shard;
// synthesize aggregates per-decision verdicts plus any parent-prose
// shard into a strategy-level verdict.
//
// per_decision uses RunItem so each fanout slot fires the legacy 3-call
// sub-flow (challenger → research → advocate via existing helpers).
// Sequential (Parallel: false) preserves the legacy ordering: the
// existing fanout test scripts mock responses in per-decision-then-
// per-step order, and grounded researcher calls competing in parallel
// for provider rate budgets has caused trouble in the past.
//
// synthesize is conditional on the per_decision step producing
// results OR the splitter emitting a non-empty parent_prose_shard.
// The existing InvokeSynthesizer guard rejects "nothing to
// synthesize" — gating it at the workflow layer avoids the wasted
// dispatch and keeps the verb's failure surface clean.
var JustifyAdversarialFanoutWorkflow = &Workflow[JustifyState]{
	Snapshot: snapshotJustifyState,
	Rounds: []WorkflowStep[JustifyState]{
		{
			ID:      "classify",
			Agents:  []string{"justify_splitter"},
			RunItem: runJustifyClassify,
			Merge:   mergeJustifyClassify,
		},
		{
			ID:        "per_decision",
			Agents:    []string{"spec_advocate"},
			DependsOn: []string{"classify"},
			Fanout:    fanoutJustifyDecisions,
			RunItem:   runJustifyPerDecision,
			Merge:     mergeJustifyPerDecision,
		},
		{
			ID:          "synthesize",
			Agents:      []string{"justify_synthesizer"},
			DependsOn:   []string{"per_decision"},
			Conditional: hasSynthesisInputs,
			RunItem:     runJustifySynthesize,
			Merge:       mergeJustifySynthesize,
		},
	},
	MaxRounds: 1,
}

// runJustifySoloDefend dispatches the spec_advocate against the
// rendered node + GOALS. Mirrors the old RunJustify path verbatim —
// same prompt builder, same dispatch options, same parse + emptiness
// check.
func runJustifySoloDefend(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	if st.NodeMarkdown == "" {
		return "", fmt.Errorf("justify: empty node content for %q", st.NodeID)
	}
	in := JustifyInputs{
		NodeID:       st.NodeID,
		NodeMarkdown: st.NodeMarkdown,
		GoalsBody:    st.GoalsBody,
	}
	user := buildAdvocateUserMessage(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	resp, err := st.Dispatcher.Dispatch(ctx, st.AdvocateDef, input, DispatchOptions{
		Role:         "justification",
		OutputSchema: "JustificationBrief",
	})
	if err != nil {
		return "", fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	var brief JustificationBrief
	if perr := unmarshalAgentOutput(resp.Content, &brief); perr != nil {
		return "", fmt.Errorf("justify: advocate response: %w", perr)
	}
	if brief.Defense == "" {
		return "", fmt.Errorf("justify: advocate returned empty defense for %q", st.NodeID)
	}
	data, mErr := json.Marshal(&brief)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal solo brief: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifySoloDefend stashes the parsed JustificationBrief on
// state so the cmd layer can read it back. Re-parses from the
// RoundResult Output (raw JSON) to keep the merge handler decoupled
// from RunItem's internal parse.
func mergeJustifySoloDefend(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		return
	}
	var brief JustificationBrief
	if err := unmarshalAgentOutput(out, &brief); err != nil {
		return
	}
	s.Brief = &brief
}

// runJustifyChallenge dispatches the spec_challenger via the
// degeneracy-retry helper. Its return value is the canonical JSON of
// the ChallengeBrief.
func runJustifyChallenge(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	if st.Challenge == "" {
		return "", fmt.Errorf("justify: empty challenge")
	}
	if st.NodeMarkdown == "" {
		return "", fmt.Errorf("justify: empty node content for %q", st.NodeID)
	}
	in := JustifyInputs{
		NodeID:       st.NodeID,
		NodeMarkdown: st.NodeMarkdown,
		GoalsBody:    st.GoalsBody,
		Challenge:    st.Challenge,
	}
	challengeUser := buildChallengerUserMessage(in)
	challengeInput := AgentInput{Messages: []Message{{Role: "user", Content: challengeUser}}}

	challenge, err := dispatchChallengerWithRetry(ctx, st.Dispatcher, st.ChallengerDef, challengeInput, st.NodeID)
	if err != nil {
		// Surface the partial brief alongside the error so the cmd
		// layer can still render what the model emitted on terminal
		// failure (matches RunJustifyAgainst's behavior).
		var data []byte
		if challenge != nil {
			data, _ = json.Marshal(challenge)
		}
		return string(data), err
	}
	data, mErr := json.Marshal(challenge)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal challenge brief: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifyChallenge stores the parsed ChallengeBrief on state so
// the research and defend steps can read it.
func mergeJustifyChallenge(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		// Even on RunItem error the helper returns the partial
		// challenge so the cmd layer can show it; pull from the
		// first result regardless of Err.
		for _, r := range results {
			if r.Output != "" {
				out = r.Output
				break
			}
		}
	}
	if out == "" {
		return
	}
	var brief ChallengeBrief
	if err := unmarshalAgentOutput(out, &brief); err != nil {
		return
	}
	s.ChallengerOut = &brief
}

// researchEnvelope wraps ResearchBrief with its post-call ToolOutcomes
// metadata for transport through the workflow's RoundResult.Output
// JSON. ResearchBrief.ToolOutcomes is `json:"-"` (the model never
// emits it; RunResearch stamps it from the adapter's per-call
// metadata), so a plain Marshal would drop the data. The merge
// handler unmarshals this envelope and re-stamps ToolOutcomes on the
// brief stored in state.
//
// The advocate's prompt builder reads ResearcherOut.FailedQueries()
// to render an explicit ungrounded-queries section; losing
// ToolOutcomes would silently regress that warning surface in the
// adversarial workflow paths.
type researchEnvelope struct {
	Brief        ResearchBrief `json:"brief"`
	ToolOutcomes []ToolCall    `json:"tool_outcomes,omitempty"`
}

// runJustifyResearch dispatches the grounded researcher.
func runJustifyResearch(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	in := JustifyInputs{
		NodeID:        st.NodeID,
		NodeMarkdown:  st.NodeMarkdown,
		GoalsBody:     st.GoalsBody,
		Challenge:     st.Challenge,
		ChallengerOut: st.ChallengerOut,
		Researcher:    st.ResearcherDef,
	}
	research, err := RunResearch(ctx, st.Dispatcher, in)
	if err != nil {
		return "", err
	}
	envelope := researchEnvelope{Brief: *research, ToolOutcomes: research.ToolOutcomes}
	data, mErr := json.Marshal(envelope)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal research brief: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifyResearch stores the parsed ResearchBrief on state with
// ToolOutcomes preserved via the researchEnvelope wrapper.
func mergeJustifyResearch(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		return
	}
	var envelope researchEnvelope
	if err := unmarshalAgentOutput(out, &envelope); err != nil {
		return
	}
	brief := envelope.Brief
	brief.ToolOutcomes = envelope.ToolOutcomes
	s.ResearcherOut = &brief
}

// runJustifyAdversarialDefend dispatches the spec_advocate with the
// challenger + researcher context and returns the canonical
// AdversarialDefense JSON.
func runJustifyAdversarialDefend(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	in := JustifyInputs{
		NodeID:        st.NodeID,
		NodeMarkdown:  st.NodeMarkdown,
		GoalsBody:     st.GoalsBody,
		Challenge:     st.Challenge,
		ChallengerOut: st.ChallengerOut,
		ResearcherOut: st.ResearcherOut,
	}
	advocateUser := buildAdvocateUserMessage(in)
	advocateInput := AgentInput{Messages: []Message{{Role: "user", Content: advocateUser}}}

	resp, err := st.Dispatcher.Dispatch(ctx, st.AdvocateDef, advocateInput, DispatchOptions{
		Role:         "justification",
		OutputSchema: "AdversarialDefense",
	})
	if err != nil {
		return "", fmt.Errorf("justify: advocate dispatch: %w", err)
	}
	var defense AdversarialDefense
	if perr := unmarshalAgentOutput(resp.Content, &defense); perr != nil {
		return "", fmt.Errorf("justify: advocate response: %w", perr)
	}
	if defense.Defense == "" {
		data, _ := json.Marshal(&defense)
		return string(data), fmt.Errorf("justify: advocate returned empty defense for %q", st.NodeID)
	}
	if !validVerdict(defense.Verdict) {
		data, _ := json.Marshal(&defense)
		return string(data), fmt.Errorf("justify: advocate returned invalid verdict %q (want held_up|partially_held_up|broke_down)", defense.Verdict)
	}
	data, mErr := json.Marshal(&defense)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal adversarial defense: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifyAdversarialDefend stashes the parsed AdversarialDefense
// on state so the cmd layer can render it.
func mergeJustifyAdversarialDefend(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		// Surface partial defense even on error (e.g. invalid verdict
		// returned from RunItem with both output and error).
		for _, r := range results {
			if r.Output != "" {
				out = r.Output
				break
			}
		}
	}
	if out == "" {
		return
	}
	var defense AdversarialDefense
	if err := unmarshalAgentOutput(out, &defense); err != nil {
		return
	}
	s.AdversarialDefense = &defense
}

// runJustifyClassify dispatches the splitter via InvokeSplitter and
// returns the canonical ChallengeSplit JSON.
func runJustifyClassify(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	in := SplitterInput{
		ParentID:    st.NodeID,
		ParentKind:  nodeKindFromID(st.NodeID),
		ParentTitle: parentTitleOrID(st),
		ParentProse: st.ParentBody,
		Decisions:   st.DecisionRefs,
		Challenge:   st.Challenge,
	}
	split, err := InvokeSplitter(ctx, st.Dispatcher, st.SplitterDef, in)
	if err != nil {
		return "", fmt.Errorf("justify fan-out: split: %w", err)
	}
	data, mErr := json.Marshal(split)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal split: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifyClassify stores the parsed split on state so per_decision
// can read DecisionShards / ParentProseShard.
func mergeJustifyClassify(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		return
	}
	var split ChallengeSplit
	if err := unmarshalAgentOutput(out, &split); err != nil {
		return
	}
	s.Split = &split
}

// fanoutJustifyDecisions returns one fanout item per non-empty
// decision shard. Empty shards mean the splitter determined the
// challenge doesn't engage that decision — skip rather than burn
// tokens on a forced run. Mirrors the legacy RunJustifyFanOut filter.
//
// The fanout item is the JSON of fanoutDecisionItem (decision id +
// title + per-decision markdown + shard text); the per-slot RunItem
// closure uses these to assemble the per-decision JustifyInputs.
func fanoutJustifyDecisions(state *JustifyState) ([]string, error) {
	if state == nil || state.Split == nil {
		return nil, nil
	}
	items := make([]any, 0, len(state.Split.DecisionShards))
	for i, shard := range state.Split.DecisionShards {
		if strings.TrimSpace(shard.Shard) == "" {
			continue
		}
		if i >= len(state.DecisionRefs) {
			// Splitter validation already enforces shard-count match
			// against DecisionRefs, but a defensive bounds check costs
			// nothing.
			continue
		}
		ref := state.DecisionRefs[i]
		items = append(items, fanoutDecisionItem{
			ID:           ref.ID,
			Title:        ref.Title,
			NodeMarkdown: state.PerDecisionMarkdown[ref.ID],
			Shard:        shard.Shard,
		})
	}
	return marshalFanoutItems(items)
}

// fanoutDecisionItem is the per-slot input payload for the per_decision
// fanout step. Carries everything the per-slot RunItem closure needs
// to dispatch a 3-call sub-flow against one decision.
type fanoutDecisionItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	NodeMarkdown string `json:"node_markdown"`
	Shard        string `json:"shard"`
}

// runJustifyPerDecision is the per-slot closure for the per_decision
// fanout step. Each fanout slot dispatches the existing 3-call
// adversarial sub-flow (challenger → research → advocate) against one
// decision shard via the existing dispatcher-driven helpers.
//
// Returns the canonical FanOutDecisionResult JSON as the slot's
// RoundResult Output; the merge handler unmarshals and appends to
// state.PerDecisionResults.
//
// Aborts on first per-decision failure (matches the legacy
// RunJustifyFanOut behavior — half-failed fan-out runs aren't
// synthesized; surface the error so the user can re-run or scope).
func runJustifyPerDecision(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	if snap.FanoutItem == "" {
		return "", fmt.Errorf("per_decision: empty fanout item")
	}
	var item fanoutDecisionItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		return "", fmt.Errorf("per_decision: parse fanout item: %w", err)
	}
	perIn := JustifyInputs{
		NodeID:       item.ID,
		NodeMarkdown: item.NodeMarkdown,
		GoalsBody:    st.GoalsBody,
		Challenge:    item.Shard,
		Advocate:     st.AdvocateDef,
		Challenger:   st.ChallengerDef,
		Researcher:   st.ResearcherDef,
	}
	challenger, research, defense, err := RunJustifyAgainst(ctx, st.Dispatcher, perIn)
	if err != nil {
		return "", fmt.Errorf("decision %s: %w", item.ID, err)
	}
	result := FanOutDecisionResult{
		DecisionID:    item.ID,
		DecisionTitle: item.Title,
		Challenge:     item.Shard,
		Challenger:    challenger,
		Research:      research,
		Adversarial:   defense,
	}
	data, mErr := json.Marshal(&result)
	if mErr != nil {
		return "", fmt.Errorf("per_decision: marshal result: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifyPerDecision appends each per-slot FanOutDecisionResult
// to state.PerDecisionResults in fanout order.
func mergeJustifyPerDecision(s *JustifyState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		var item FanOutDecisionResult
		if err := unmarshalAgentOutput(r.Output, &item); err != nil {
			continue
		}
		s.PerDecisionResults = append(s.PerDecisionResults, item)
	}
}

// hasSynthesisInputs gates the synthesize step. Mirrors the
// InvokeSynthesizer guard: there must be either per-decision results
// OR a non-empty parent_prose_shard for synthesis to be meaningful.
// Skipping the step when both are empty avoids a wasted dispatch and
// the "nothing to synthesize" error surfacing as a workflow failure.
func hasSynthesisInputs(s *JustifyState) bool {
	if s == nil {
		return false
	}
	if len(s.PerDecisionResults) > 0 {
		return true
	}
	if s.Split != nil && strings.TrimSpace(s.Split.ParentProseShard) != "" {
		return true
	}
	return false
}

// runJustifySynthesize dispatches the synthesizer via
// InvokeSynthesizer and returns the canonical SynthesisVerdict JSON.
func runJustifySynthesize(ctx context.Context, snap StateSnapshot[JustifyState]) (string, error) {
	st := snap.State
	if st.Split == nil {
		return "", fmt.Errorf("synthesize: split missing — classify must run first")
	}
	in := SynthesisInput{
		ParentID:           st.NodeID,
		ParentKind:         nodeKindFromID(st.NodeID),
		ParentTitle:        parentTitleOrID(st),
		ParentProse:        st.ParentBody,
		GoalsBody:          st.GoalsBody,
		Challenge:          st.Challenge,
		PerDecisionResults: toSynthInputs(st.PerDecisionResults),
		ParentProseShard:   st.Split.ParentProseShard,
	}
	synth, err := InvokeSynthesizer(ctx, st.Dispatcher, st.SynthesizerDef, in)
	if err != nil {
		return "", fmt.Errorf("justify fan-out: synthesize: %w", err)
	}
	data, mErr := json.Marshal(synth)
	if mErr != nil {
		return "", fmt.Errorf("justify: marshal synthesis: %w", mErr)
	}
	return string(data), nil
}

// mergeJustifySynthesize stores the parsed verdict on state.
func mergeJustifySynthesize(s *JustifyState, results []RoundResult) {
	out := firstNonEmpty(results)
	if out == "" {
		return
	}
	var verdict SynthesisVerdict
	if err := unmarshalAgentOutput(out, &verdict); err != nil {
		return
	}
	s.Synthesis = &verdict
}

// parentTitleOrID returns the parent's title from the rendered explain
// output if extractable, falling back to the node id. Used by the
// classify and synthesize steps to give the splitter / synthesizer a
// human-readable parent title in their prompts.
func parentTitleOrID(s JustifyState) string {
	if s.ParentNodeMD != "" {
		if title := firstH1Title(s.ParentNodeMD); title != "" {
			return title
		}
	}
	return s.NodeID
}
