package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SupersedeState is the workflow blackboard for `refine --supersede`
// (Phase 8 of workflow unification). Two phases:
//
//   - emit_replacement (fanout-of-one): dispatch the kind-appropriate
//     refiner-supersede-<kind> agent; the merge handler parses the
//     replacement node, computes the cascade plan, applies it
//     atomically, and reloads the post-cascade graph for phase 2.
//   - prose_cascade (fanout): per affected downstream parent, refresh
//     prose via the refiner agent so wording stops referencing the
//     superseded node. Skipped (empty fanout) for in-place supersedes.
//
// The workflow lives in cmd/ rather than internal/agent/ because the
// merge handler needs cascade.ComputeSupersedePlan + ApplySupersede*,
// and internal/cascade already imports internal/agent — putting the
// workflow in internal/agent would create a cycle, and the cascade
// helpers (~400 lines) are too large to mirror Phase 7's
// duplicate-in-agent pattern. The Phase 7 reason for in-agent
// placement doesn't apply at this duplication ratio.
type SupersedeState struct {
	// Inputs (populated by RunRefineSupersede before workflow.Run).
	OldID          string
	Kind           spec.NodeKind
	Motivation     string
	JustifySession string
	EventID        string
	FSys           specio.FS
	Loaded         *spec.Loaded

	// Exactly one of these is non-nil, keyed by Kind.
	OldDecision *spec.Decision
	OldFeature  *spec.Feature
	OldStrategy *spec.Strategy

	// Phase 1 outputs.
	Plan           *cascade.SupersedePlan
	Event          *history.Event
	AgentRationale string

	// ReplacementError carries the specific reason phase 1 failed
	// (parse error, id-prefix violation, cascade compute/apply error)
	// so the cmd layer can surface a precise message instead of a
	// generic "no cascade applied". The workflow merge handler can't
	// return errors directly, so we plumb the cause through state.
	ReplacementError error

	// Reload of the spec after the mechanical cascade applies, used by
	// phase 2 to resolve current decision references on downstream
	// parents. nil when phase 1 failed or InPlace short-circuited.
	PostCascade *spec.Loaded

	// Phase 2 outputs.
	ProseFailures []string
}

// supersedeReplaceItem is the single fanout-of-one item driving
// phase 1's agent dispatch. AgentID is kind-routed
// (refiner-supersede-decision / -feature / -strategy) via DJ-098's
// per-item dispatch — the workflow itself stays single-shape across
// kinds, and the cmd layer picks the right agent through the fanout
// instead of running three workflow vars.
type supersedeReplaceItem struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
}

// supersedeProseItem identifies one downstream parent whose prose
// needs refresh after the mechanical cascade. Kind is "feature",
// "strategy", or "bug"; ID matches the parent in the post-cascade
// graph.
type supersedeProseItem struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
}

// SupersedeWorkflow is the two-phase supersede workflow. MaxRounds=1
// because the verb has no convergence loop — both phases run once.
var SupersedeWorkflow = &agent.Workflow[SupersedeState]{
	Snapshot: snapshotSupersedeState,
	Rounds: []agent.WorkflowStep[SupersedeState]{
		{
			ID:       "emit_replacement",
			Agents:   []string{"refiner-supersede-decision"}, // fallback; real choice via fanout agent_id
			Parallel: false,
			Fanout:   fanoutSupersedeKind,
			Project:  projectSupersedeReplace,
			Merge:    mergeSupersedeReplace,
		},
		{
			ID:       "prose_cascade",
			Agents:   []string{"refiner"},
			Parallel: false, // mirrors legacy runProseCascade sequential ordering
			Fanout:   fanoutSupersedeProse,
			Project:  projectSupersedeProse,
			Merge:    mergeSupersedeProse,
		},
	},
	MaxRounds: 1,
}

// snapshotSupersedeState produces a shallow value copy. Pointer fields
// (Loaded, PostCascade, OldDecision/Feature/Strategy, Plan, Event) are
// shared by reference; the merge handler is the sole writer for those,
// and Parallel=false means projection reads never race with merges.
func snapshotSupersedeState(s *SupersedeState) SupersedeState {
	if s == nil {
		return SupersedeState{}
	}
	return *s
}

// fanoutSupersedeKind emits the single phase-1 item carrying the
// kind-routed agent id. Returns nil for unsupported kinds — RunRefineSupersede
// rejects those before invoking the workflow, so this branch is
// defensive against invariant violation by the caller.
func fanoutSupersedeKind(s *SupersedeState) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	agentID := supersedeAgentForKind(s.Kind)
	if agentID == "" {
		return nil, fmt.Errorf("supersede workflow: unsupported kind %q", s.Kind)
	}
	item := supersedeReplaceItem{AgentID: agentID, Kind: string(s.Kind)}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("supersede workflow: marshal kind item: %w", err)
	}
	return []string{string(raw)}, nil
}

// supersedeAgentForKind maps the target's spec kind to the
// kind-specific refiner-supersede agent. Empty for bugs (rejected by
// the cmd layer) and any other kind (defense in depth).
func supersedeAgentForKind(kind spec.NodeKind) string {
	switch kind {
	case spec.KindDecision:
		return "refiner-supersede-decision"
	case spec.KindFeature:
		return "refiner-supersede-feature"
	case spec.KindStrategy:
		return "refiner-supersede-strategy"
	}
	return ""
}

// projectSupersedeReplace renders the kind-appropriate prompt for
// phase 1. Delegates to the existing prompt builders in
// internal/agent/refiner_supersede.go (exported by the cmd-internal
// wrapper buildSupersede<kind>Prompt below) so the prompt body stays
// in lockstep with the direct-call path's invocations.
func projectSupersedeReplace(snap agent.StateSnapshot[SupersedeState]) []agent.Message {
	st := snap.State
	switch st.Kind {
	case spec.KindDecision:
		if st.OldDecision == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		prompt := agent.BuildSupersedeDecisionPrompt(*st.OldDecision, st.Motivation, st.JustifySession)
		return []agent.Message{{Role: "user", Content: prompt}}
	case spec.KindFeature:
		if st.OldFeature == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		prompt := agent.BuildSupersedeFeaturePrompt(*st.OldFeature, st.Motivation, st.JustifySession)
		return []agent.Message{{Role: "user", Content: prompt}}
	case spec.KindStrategy:
		if st.OldStrategy == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		prompt := agent.BuildSupersedeStrategyPrompt(*st.OldStrategy, st.Motivation, st.JustifySession)
		return []agent.Message{{Role: "user", Content: prompt}}
	}
	return []agent.Message{{Role: "user", Content: ""}}
}

// mergeSupersedeReplace parses the LLM output, validates the id
// prefix, runs ComputeSupersedePlan + ApplySupersede<kind>, and stores
// the resulting plan + event + agent rationale on state. Mirrors
// runSupersede<kind>'s post-LLM logic in cmd/refine_supersede.go.
//
// On failure (parse error, missing id, cascade error) the function
// logs and returns without populating state.Plan — the cmd layer's
// post-Run check surfaces this as an error to the caller. We don't
// abort the workflow here because the executor's merge contract is
// "best effort to apply results"; the cmd layer interprets a nil Plan
// as the failure signal.
func mergeSupersedeReplace(s *SupersedeState, results []agent.RoundResult) {
	if s == nil || len(results) == 0 {
		return
	}
	r := results[0]
	if r.Err != nil {
		slog.Warn("supersede workflow: phase 1 agent failed", "error", r.Err)
		return
	}
	if r.Output == "" {
		slog.Warn("supersede workflow: phase 1 agent returned empty output")
		return
	}

	hist := history.NewHistorian(s.FSys, ".borg/history")

	switch s.Kind {
	case spec.KindDecision:
		s.applyDecisionReplacement(r.Output, hist)
	case spec.KindFeature:
		s.applyFeatureReplacement(r.Output, hist)
	case spec.KindStrategy:
		s.applyStrategyReplacement(r.Output, hist)
	default:
		slog.Warn("supersede workflow: unsupported kind in merge", "kind", s.Kind)
		return
	}

	if s.Plan == nil {
		return
	}
	// Reload the spec graph after the mechanical cascade so phase 2's
	// fanout / projection reads current id references in features /
	// strategies / bugs. Soft-degrades on error — phase 2's fanout
	// guards against PostCascade==nil by falling back to Loaded, which
	// keeps the structured cascade intact even if prose refresh is
	// skipped.
	post, err := spec.LoadSpec(s.FSys)
	if err != nil {
		slog.Warn("supersede workflow: post-cascade reload failed", "error", err)
		return
	}
	s.PostCascade = post
}

func (s *SupersedeState) applyDecisionReplacement(output string, hist *history.Historian) {
	var result agent.RewriteDecisionResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.ReplacementError = fmt.Errorf("parse decision result: %w", err)
		return
	}
	newDec := result.RevisedDecision
	if strings.TrimSpace(newDec.ID) == "" {
		s.ReplacementError = fmt.Errorf("decision agent returned empty id")
		return
	}
	if !strings.HasPrefix(newDec.ID, "dec-") {
		s.ReplacementError = fmt.Errorf("agent emitted id %q without dec- prefix", newDec.ID)
		return
	}
	plan, err := cascade.ComputeSupersedePlan(s.Loaded, s.OldID, newDec.ID, s.EventID)
	if err != nil {
		s.ReplacementError = fmt.Errorf("compute decision plan: %w", err)
		return
	}
	evt, err := cascade.ApplySupersedeDecision(s.FSys, plan, newDec, s.Motivation, s.JustifySession, hist)
	if err != nil {
		s.ReplacementError = fmt.Errorf("apply decision cascade: %w", err)
		return
	}
	s.Plan = plan
	s.Event = evt
	s.AgentRationale = result.Rationale
}

func (s *SupersedeState) applyFeatureReplacement(output string, hist *history.Historian) {
	var result agent.RewriteFeatureResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.ReplacementError = fmt.Errorf("parse feature result: %w", err)
		return
	}
	newFeat := result.RevisedFeature
	if strings.TrimSpace(newFeat.ID) == "" {
		s.ReplacementError = fmt.Errorf("feature agent returned empty id")
		return
	}
	if !strings.HasPrefix(newFeat.ID, "feat-") {
		s.ReplacementError = fmt.Errorf("agent emitted id %q without feat- prefix", newFeat.ID)
		return
	}
	plan, err := cascade.ComputeSupersedePlan(s.Loaded, s.OldID, newFeat.ID, s.EventID)
	if err != nil {
		s.ReplacementError = fmt.Errorf("compute feature plan: %w", err)
		return
	}
	evt, err := cascade.ApplySupersedeFeature(s.FSys, plan, newFeat, s.Motivation, s.JustifySession, hist)
	if err != nil {
		s.ReplacementError = fmt.Errorf("apply feature cascade: %w", err)
		return
	}
	s.Plan = plan
	s.Event = evt
	s.AgentRationale = result.Rationale
}

func (s *SupersedeState) applyStrategyReplacement(output string, hist *history.Historian) {
	var result agent.RewriteStrategyResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.ReplacementError = fmt.Errorf("parse strategy result: %w", err)
		return
	}
	newStrat := result.RevisedStrategy
	if strings.TrimSpace(newStrat.ID) == "" {
		s.ReplacementError = fmt.Errorf("strategy agent returned empty id")
		return
	}
	if !strings.HasPrefix(newStrat.ID, "strat-") {
		s.ReplacementError = fmt.Errorf("agent emitted id %q without strat- prefix", newStrat.ID)
		return
	}
	plan, err := cascade.ComputeSupersedePlan(s.Loaded, s.OldID, newStrat.ID, s.EventID)
	if err != nil {
		s.ReplacementError = fmt.Errorf("compute strategy plan: %w", err)
		return
	}
	evt, err := cascade.ApplySupersedeStrategy(s.FSys, plan, newStrat, s.Motivation, s.JustifySession, hist)
	if err != nil {
		s.ReplacementError = fmt.Errorf("apply strategy cascade: %w", err)
		return
	}
	s.Plan = plan
	s.Event = evt
	s.AgentRationale = result.Rationale
}

// fanoutSupersedeProse emits one item per downstream parent whose
// prose needs refresh. Returns nil (empty fanout) when the plan is
// in-place — id references didn't change, so the refiner has nothing
// to rewrite against. Empty fanout short-circuits phase 2 entirely.
func fanoutSupersedeProse(s *SupersedeState) ([]string, error) {
	if s == nil || s.Plan == nil {
		return nil, nil
	}
	if s.Plan.InPlace {
		return nil, nil
	}
	items := make([]any, 0, len(s.Plan.FeaturesToRewrite)+len(s.Plan.StrategiesToRewrite)+len(s.Plan.BugsToRewrite))
	for _, fid := range s.Plan.FeaturesToRewrite {
		items = append(items, supersedeProseItem{AgentID: "refiner", Kind: "feature", ID: fid})
	}
	for _, sid := range s.Plan.StrategiesToRewrite {
		items = append(items, supersedeProseItem{AgentID: "refiner", Kind: "strategy", ID: sid})
	}
	for _, bid := range s.Plan.BugsToRewrite {
		items = append(items, supersedeProseItem{AgentID: "refiner", Kind: "bug", ID: bid})
	}
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		raw, err := json.Marshal(it)
		if err != nil {
			return nil, fmt.Errorf("supersede workflow: marshal prose item: %w", err)
		}
		out = append(out, string(raw))
	}
	return out, nil
}

// projectSupersedeProse renders the refiner prompt for one downstream
// parent. Uses the post-cascade spec graph so id references already
// reflect the replacement. The motivation acts as the refiner's
// brief — phase 2 is intent-driven prose refresh, not cascade rewrite,
// so the refiner agent (not rewriter) is the right tool.
func projectSupersedeProse(snap agent.StateSnapshot[SupersedeState]) []agent.Message {
	var item supersedeProseItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		slog.Warn("supersede workflow: malformed prose fanout item", "error", err, "raw", snap.FanoutItem)
		return []agent.Message{{Role: "user", Content: ""}}
	}
	st := snap.State
	loaded := st.PostCascade
	if loaded == nil {
		loaded = st.Loaded
	}
	if loaded == nil {
		return []agent.Message{{Role: "user", Content: ""}}
	}

	var (
		parentKind  string
		parentID    string
		parentTitle string
		currentBody string
		applicable  []spec.Decision
	)

	switch item.Kind {
	case "feature":
		f := loaded.FeatureNodeByID(item.ID)
		if f == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		parentKind = "feature"
		parentID = f.Spec.ID
		parentTitle = f.Spec.Title
		currentBody = f.Spec.Description
		applicable = resolveSupersedeDecisions(loaded, f.Spec.Decisions)
	case "strategy":
		sNode := loaded.StrategyNodeByID(item.ID)
		if sNode == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		body, _ := st.FSys.ReadFile(".borg/spec/strategies/" + sNode.Spec.ID + ".md")
		parentKind = "strategy"
		parentID = sNode.Spec.ID
		parentTitle = sNode.Spec.Title
		currentBody = string(body)
		applicable = resolveSupersedeDecisions(loaded, sNode.Spec.Decisions)
	case "bug":
		b := loaded.BugNodeByID(item.ID)
		if b == nil {
			return []agent.Message{{Role: "user", Content: ""}}
		}
		parentKind = "bug"
		parentID = b.Spec.ID
		parentTitle = b.Spec.Title
		currentBody = b.Spec.Description
		// Bugs inherit decisions from their parent feature, mirroring
		// the legacy runProseCascade.
		if pf := loaded.FeatureNodeByID(b.Spec.FeatureID); pf != nil {
			applicable = resolveSupersedeDecisions(loaded, pf.Spec.Decisions)
		}
	default:
		return []agent.Message{{Role: "user", Content: ""}}
	}

	// Same prompt shape as cascade.invokeRewriter (the legacy path).
	// Motivation flows in as the brief so the refiner — not the
	// rewriter — picks up the call.
	prompt := buildSupersedeRefinerPrompt(parentKind, parentID, parentTitle, currentBody, st.Motivation, applicable)
	return []agent.Message{{Role: "user", Content: prompt}}
}

// mergeSupersedeProse processes each prose-cascade result, persisting
// the revised body to the same path cascade.RewriteFeature/Strategy/Bug
// would. Failures land in state.ProseFailures so the cmd-layer summary
// can surface them; they don't abort the workflow (the structured
// supersede already landed; stale prose is a soft regression).
func mergeSupersedeProse(s *SupersedeState, results []agent.RoundResult) {
	if s == nil {
		return
	}
	items, err := fanoutSupersedeProse(s)
	if err != nil {
		slog.Warn("supersede workflow: re-emit prose items failed in merge", "error", err)
		return
	}
	if len(items) != len(results) {
		slog.Warn("supersede workflow: prose results length differs from fanout items",
			"items", len(items), "results", len(results))
	}
	n := len(items)
	if len(results) < n {
		n = len(results)
	}

	for i := 0; i < n; i++ {
		var item supersedeProseItem
		if err := json.Unmarshal([]byte(items[i]), &item); err != nil {
			slog.Warn("supersede workflow: malformed prose item in merge", "error", err)
			continue
		}
		r := results[i]
		if r.Err != nil {
			slog.Warn("supersede workflow: refiner failed for parent",
				"kind", item.Kind, "id", item.ID, "error", r.Err)
			s.ProseFailures = append(s.ProseFailures, item.ID)
			continue
		}
		if r.Output == "" {
			continue
		}
		s.persistSupersedeProseResult(item, r.Output)
	}
}

// persistSupersedeProseResult parses one refiner output and writes the
// revised body via specio.SavePair — the same persistence path
// cascade.RewriteFeature/Strategy/Bug use. Unchanged outputs skip the
// write so the equivalence test sees the same UpdatedAt drift the
// legacy path produces.
func (s *SupersedeState) persistSupersedeProseResult(item supersedeProseItem, output string) {
	var rw struct {
		RevisedBody string `json:"revised_body"`
		Changed     bool   `json:"changed"`
		Rationale   string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(output), &rw); err != nil {
		slog.Warn("supersede workflow: parse prose result failed",
			"kind", item.Kind, "id", item.ID, "error", err)
		s.ProseFailures = append(s.ProseFailures, item.ID)
		return
	}
	loaded := s.PostCascade
	if loaded == nil {
		loaded = s.Loaded
	}
	if loaded == nil {
		return
	}

	switch item.Kind {
	case "feature":
		f := loaded.FeatureNodeByID(item.ID)
		if f == nil {
			return
		}
		if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(f.Spec.Description) {
			return
		}
		updated := f.Spec
		updated.Description = rw.RevisedBody
		updated.UpdatedAt = time.Now()
		if err := specio.SavePair(s.FSys, ".borg/spec/features/"+f.Spec.ID, updated, rw.RevisedBody); err != nil {
			slog.Warn("supersede workflow: save feature prose failed",
				"id", item.ID, "error", err)
			s.ProseFailures = append(s.ProseFailures, item.ID)
		}
	case "strategy":
		sNode := loaded.StrategyNodeByID(item.ID)
		if sNode == nil {
			return
		}
		currentBody, _ := s.FSys.ReadFile(".borg/spec/strategies/" + sNode.Spec.ID + ".md")
		if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(string(currentBody)) {
			return
		}
		if err := specio.SavePair(s.FSys, ".borg/spec/strategies/"+sNode.Spec.ID, sNode.Spec, rw.RevisedBody); err != nil {
			slog.Warn("supersede workflow: save strategy prose failed",
				"id", item.ID, "error", err)
			s.ProseFailures = append(s.ProseFailures, item.ID)
		}
	case "bug":
		b := loaded.BugNodeByID(item.ID)
		if b == nil {
			return
		}
		if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(b.Spec.Description) {
			return
		}
		updated := b.Spec
		updated.Description = rw.RevisedBody
		updated.UpdatedAt = time.Now()
		if err := specio.SavePair(s.FSys, ".borg/spec/bugs/"+b.Spec.ID, updated, rw.RevisedBody); err != nil {
			slog.Warn("supersede workflow: save bug prose failed",
				"id", item.ID, "error", err)
			s.ProseFailures = append(s.ProseFailures, item.ID)
		}
	}
}

// buildSupersedeRefinerPrompt mirrors cascade.invokeRewriter's prompt
// shape. Brief is always non-empty (it's the motivation), so the
// "## Recently changed Decisions" block from cascade-mode is omitted —
// the refiner's intent comes from the brief, not from a recent-change
// list. The "## Refinement intent" framing matches what the legacy
// runProseCascade emitted via cascade.WithBrief(ctx, motivation).
func buildSupersedeRefinerPrompt(parentKind, parentID, parentTitle, currentBody, motivation string, applicable []spec.Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Refinement intent\n%s\n\n", motivation)
	fmt.Fprintf(&b, "## Parent kind\n%s\n\n", parentKind)
	fmt.Fprintf(&b, "## Parent ID\n%s\n\n", parentID)
	fmt.Fprintf(&b, "## Parent title\n%s\n\n", parentTitle)
	b.WriteString("## Current parent prose\n")
	b.WriteString(currentBody)
	b.WriteString("\n\n## Applicable Decisions\n")
	for _, d := range applicable {
		fmt.Fprintf(&b, "- %s (%s, confidence=%.2f): %s — %s\n", d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
	}
	return b.String()
}

// resolveSupersedeDecisions resolves a slice of decision ids against
// the post-cascade graph, mirroring resolveDecisions in
// refine_supersede.go. Skips ids whose decision is missing.
func resolveSupersedeDecisions(loaded *spec.Loaded, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d := loaded.DecisionNodeByID(id); d != nil {
			out = append(out, d.Spec)
		}
	}
	return out
}
