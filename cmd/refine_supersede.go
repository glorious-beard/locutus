package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SupersedeSummary is the result payload for the --supersede path.
// Mirrors the cascade buckets the user can act on; the durable record
// lives in the .borg/history event referenced by EventID.
type SupersedeSummary struct {
	OldID      string `json:"old_id"`
	NewID      string `json:"new_id"`
	NodeKind   string `json:"node_kind"`
	InPlace    bool   `json:"in_place"`
	EventID    string `json:"event_id"`
	Motivation string `json:"motivation,omitempty"`

	FeaturesRewritten              []string `json:"features_rewritten,omitempty"`
	StrategiesRewritten            []string `json:"strategies_rewritten,omitempty"`
	DecisionsInfluencedByRewritten []string `json:"decisions_influenced_by_rewritten,omitempty"`
	BugsRewritten                  []string `json:"bugs_rewritten,omitempty"`
	ApproachesInvalidated          []string `json:"approaches_invalidated,omitempty"`

	// AgentRationale carries the architect-voice summary the
	// refiner-supersede agent emitted, surfaced to operators
	// without needing to read the history event.
	AgentRationale string `json:"agent_rationale,omitempty"`
}

// RunRefineSupersede orchestrates `refine <id> --supersede "..."` via
// the SupersedeWorkflow (Phase 8 of workflow unification). The
// signature is preserved so cmd/refine.go and cmd/mcp.go keep
// compiling; the implementation now builds a SupersedeState, runs the
// two-phase workflow, and projects state into a RefineResult.
//
// justifySession is the optional .locutus/sessions/.../session.yaml
// pointer for the breaking-point analysis that motivated the
// supersede. Pass empty string when the user invoked --supersede
// directly without a preceding justify run.
func RunRefineSupersede(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, id string, kind spec.NodeKind, motivation, justifySession string, sink agent.EventSink) (*RefineResult, error) {
	if strings.TrimSpace(motivation) == "" {
		return nil, fmt.Errorf("--supersede requires a motivation argument")
	}
	if kind == spec.KindBug {
		return nil, fmt.Errorf("--supersede is not valid for bugs (%q): bugs use status transitions; file a fresh bug for wrong-root-cause cases", id)
	}
	if kind == spec.KindApproach || kind == spec.KindGoals {
		return nil, fmt.Errorf("--supersede is only valid for decisions, features, and strategies (%q is %s)", id, kind)
	}

	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, fmt.Errorf("supersede: load spec: %w", err)
	}

	state := SupersedeState{
		OldID:          id,
		Kind:           kind,
		Motivation:     motivation,
		JustifySession: justifySession,
		EventID:        history.EventID(history.EventKindNodeSuperseded, id, time.Now().UTC()),
		FSys:           fsys,
		Loaded:         loaded,
	}
	if err := bindSupersedeOldNode(&state, loaded); err != nil {
		return nil, err
	}

	defs, err := loadSupersedeAgentDefs(fsys, kind)
	if err != nil {
		return nil, fmt.Errorf("supersede: %w", err)
	}

	executor := &agent.WorkflowExecutor[SupersedeState]{
		Executor:  llm,
		AgentDefs: defs,
		Workflow:  SupersedeWorkflow,
	}
	defer executor.BridgeToSink(sink)()
	if _, err := executor.Run(ctx, &state); err != nil {
		return nil, fmt.Errorf("supersede: workflow: %w", err)
	}

	if state.ReplacementError != nil {
		return nil, fmt.Errorf("supersede: %w", state.ReplacementError)
	}
	if state.Plan == nil || state.Event == nil {
		return nil, fmt.Errorf("supersede: agent emitted unusable replacement for %s %q (no cascade applied)", kind, id)
	}
	return supersedeRefineResult(state.Plan, state.Event, state.AgentRationale, motivation), nil
}

// bindSupersedeOldNode looks up the target node and stashes it on
// state under the kind-appropriate pointer field. Returns an error
// when the id doesn't resolve — pre-LLM validation, fail fast.
func bindSupersedeOldNode(state *SupersedeState, loaded *spec.Loaded) error {
	switch state.Kind {
	case spec.KindDecision:
		n := loaded.DecisionNodeByID(state.OldID)
		if n == nil {
			return fmt.Errorf("supersede: decision %q not found", state.OldID)
		}
		dec := n.Spec
		state.OldDecision = &dec
	case spec.KindFeature:
		n := loaded.FeatureNodeByID(state.OldID)
		if n == nil {
			return fmt.Errorf("supersede: feature %q not found", state.OldID)
		}
		feat := n.Spec
		state.OldFeature = &feat
	case spec.KindStrategy:
		n := loaded.StrategyNodeByID(state.OldID)
		if n == nil {
			return fmt.Errorf("supersede: strategy %q not found", state.OldID)
		}
		strat := n.Spec
		state.OldStrategy = &strat
	default:
		return fmt.Errorf("supersede: unsupported kind %q", state.Kind)
	}
	return nil
}

// loadSupersedeAgentDefs loads the kind-specific refiner-supersede
// agent (one of three) and the refiner agent (used by phase 2's prose
// cascade). Two entries in the map covers every dispatch path the
// workflow takes for the given kind.
func loadSupersedeAgentDefs(fsys specio.FS, kind spec.NodeKind) (map[string]agent.AgentDef, error) {
	supersedeID := ""
	switch kind {
	case spec.KindDecision:
		supersedeID = "refiner-supersede-decision"
	case spec.KindFeature:
		supersedeID = "refiner-supersede-feature"
	case spec.KindStrategy:
		supersedeID = "refiner-supersede-strategy"
	default:
		return nil, fmt.Errorf("load agent defs: unsupported kind %q", kind)
	}

	defs := make(map[string]agent.AgentDef, 2)
	for _, id := range []string{supersedeID, "refiner"} {
		def, err := scaffold.LoadAgent(fsys, id)
		if err != nil {
			return nil, fmt.Errorf("load agent %s: %w", id, err)
		}
		defs[id] = def
	}
	return defs, nil
}

// printSupersedeSummary renders the operator-facing summary for a
// --supersede run. Mirrors the existing printRefineSummary verbosity
// — one-line headline plus per-bucket counts and ids.
func printSupersedeSummary(s *SupersedeSummary) {
	if s == nil {
		fmt.Println("Superseded: no result.")
		return
	}
	if s.InPlace {
		fmt.Printf("Superseded (in place) %s %s\n", s.NodeKind, s.OldID)
	} else {
		fmt.Printf("Superseded %s %s → %s\n", s.NodeKind, s.OldID, s.NewID)
	}
	if s.AgentRationale != "" {
		fmt.Printf("  Rationale: %s\n", s.AgentRationale)
	}
	if n := len(s.FeaturesRewritten); n > 0 {
		fmt.Printf("  Features rewritten: %d\n", n)
		for _, id := range s.FeaturesRewritten {
			fmt.Printf("    - %s\n", id)
		}
	}
	if n := len(s.StrategiesRewritten); n > 0 {
		fmt.Printf("  Strategies rewritten: %d\n", n)
		for _, id := range s.StrategiesRewritten {
			fmt.Printf("    - %s\n", id)
		}
	}
	if n := len(s.DecisionsInfluencedByRewritten); n > 0 {
		fmt.Printf("  Decisions (influenced_by) rewritten: %d\n", n)
		for _, id := range s.DecisionsInfluencedByRewritten {
			fmt.Printf("    - %s\n", id)
		}
	}
	if n := len(s.BugsRewritten); n > 0 {
		fmt.Printf("  Bugs (feature_id) rewritten: %d\n", n)
		for _, id := range s.BugsRewritten {
			fmt.Printf("    - %s\n", id)
		}
	}
	if n := len(s.ApproachesInvalidated); n > 0 {
		fmt.Printf("  Approaches invalidated: %d\n", n)
		for _, id := range s.ApproachesInvalidated {
			fmt.Printf("    - %s\n", id)
		}
	}
	fmt.Printf("  Event: %s\n", s.EventID)
}

func supersedeRefineResult(plan *cascade.SupersedePlan, evt *history.Event, agentRationale, motivation string) *RefineResult {
	summary := &SupersedeSummary{
		OldID:                          plan.OldID,
		NewID:                          plan.NewID,
		NodeKind:                       string(plan.NodeKind),
		InPlace:                        plan.InPlace,
		EventID:                        evt.ID,
		Motivation:                     motivation,
		FeaturesRewritten:              plan.FeaturesToRewrite,
		StrategiesRewritten:            plan.StrategiesToRewrite,
		DecisionsInfluencedByRewritten: plan.DecisionsInfluencedByToRewrite,
		BugsRewritten:                  plan.BugsToRewrite,
		ApproachesInvalidated:          plan.ApproachesToInvalidate,
		AgentRationale:                 agentRationale,
	}
	return &RefineResult{
		NodeID:    plan.OldID,
		NodeKind:  plan.NodeKind,
		Supersede: summary,
	}
}
