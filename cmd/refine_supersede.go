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

// RunRefineSupersede orchestrates `refine <id> --supersede "..."`.
// Loads the spec graph, dispatches by node kind to the matching
// LLM agent, computes the cascade plan against the agent's emitted
// replacement, and applies the plan atomically. Bug targets are
// rejected with a hint at the existing status-transition path.
//
// justifySession is the optional .locutus/sessions/.../session.yaml
// pointer for the breaking-point analysis that motivated the
// supersede. Pass empty string when the user invoked --supersede
// directly without a preceding justify run.
func RunRefineSupersede(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, id string, kind spec.NodeKind, motivation, justifySession string) (*RefineResult, error) {
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

	eventID := history.EventID(history.EventKindNodeSuperseded, id, time.Now().UTC())

	switch kind {
	case spec.KindDecision:
		return runSupersedeDecision(ctx, llm, fsys, loaded, id, motivation, justifySession, eventID)
	case spec.KindFeature:
		return runSupersedeFeature(ctx, llm, fsys, loaded, id, motivation, justifySession, eventID)
	case spec.KindStrategy:
		return runSupersedeStrategy(ctx, llm, fsys, loaded, id, motivation, justifySession, eventID)
	}
	return nil, fmt.Errorf("supersede: unsupported kind %q", kind)
}

func runSupersedeDecision(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, loaded *spec.Loaded, id, motivation, justifySession, eventID string) (*RefineResult, error) {
	old := loaded.DecisionNodeByID(id)
	if old == nil {
		return nil, fmt.Errorf("supersede: decision %q not found", id)
	}
	def, err := scaffold.LoadAgent(fsys, "refiner-supersede-decision")
	if err != nil {
		return nil, fmt.Errorf("supersede: load agent: %w", err)
	}
	result, err := agent.InvokeSupersedeDecision(ctx, llm, def, agent.SupersedeContext{
		OldNode:        &old.Spec,
		Motivation:     motivation,
		JustifySession: justifySession,
	})
	if err != nil {
		return nil, fmt.Errorf("supersede: agent: %w", err)
	}
	newDec := result.RevisedDecision
	if !strings.HasPrefix(newDec.ID, "dec-") {
		return nil, fmt.Errorf("supersede: agent emitted id %q without dec- prefix", newDec.ID)
	}

	plan, err := cascade.ComputeSupersedePlan(loaded, id, newDec.ID, eventID)
	if err != nil {
		return nil, fmt.Errorf("supersede: compute plan: %w", err)
	}
	historian := history.NewHistorian(fsys, ".borg/history")
	evt, err := cascade.ApplySupersedeDecision(fsys, plan, newDec, motivation, justifySession, historian)
	if err != nil {
		return nil, fmt.Errorf("supersede: apply: %w", err)
	}
	return supersedeRefineResult(plan, evt, result.Rationale, motivation), nil
}

func runSupersedeFeature(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, loaded *spec.Loaded, id, motivation, justifySession, eventID string) (*RefineResult, error) {
	old := loaded.FeatureNodeByID(id)
	if old == nil {
		return nil, fmt.Errorf("supersede: feature %q not found", id)
	}
	def, err := scaffold.LoadAgent(fsys, "refiner-supersede-feature")
	if err != nil {
		return nil, fmt.Errorf("supersede: load agent: %w", err)
	}
	result, err := agent.InvokeSupersedeFeature(ctx, llm, def, agent.SupersedeContext{
		OldNode:        &old.Spec,
		Motivation:     motivation,
		JustifySession: justifySession,
	})
	if err != nil {
		return nil, fmt.Errorf("supersede: agent: %w", err)
	}
	newFeat := result.RevisedFeature
	if !strings.HasPrefix(newFeat.ID, "feat-") {
		return nil, fmt.Errorf("supersede: agent emitted id %q without feat- prefix", newFeat.ID)
	}

	plan, err := cascade.ComputeSupersedePlan(loaded, id, newFeat.ID, eventID)
	if err != nil {
		return nil, fmt.Errorf("supersede: compute plan: %w", err)
	}
	historian := history.NewHistorian(fsys, ".borg/history")
	evt, err := cascade.ApplySupersedeFeature(fsys, plan, newFeat, motivation, justifySession, historian)
	if err != nil {
		return nil, fmt.Errorf("supersede: apply: %w", err)
	}
	return supersedeRefineResult(plan, evt, result.Rationale, motivation), nil
}

func runSupersedeStrategy(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, loaded *spec.Loaded, id, motivation, justifySession, eventID string) (*RefineResult, error) {
	old := loaded.StrategyNodeByID(id)
	if old == nil {
		return nil, fmt.Errorf("supersede: strategy %q not found", id)
	}
	def, err := scaffold.LoadAgent(fsys, "refiner-supersede-strategy")
	if err != nil {
		return nil, fmt.Errorf("supersede: load agent: %w", err)
	}
	result, err := agent.InvokeSupersedeStrategy(ctx, llm, def, agent.SupersedeContext{
		OldNode:        &old.Spec,
		Motivation:     motivation,
		JustifySession: justifySession,
	})
	if err != nil {
		return nil, fmt.Errorf("supersede: agent: %w", err)
	}
	newStrat := result.RevisedStrategy
	if !strings.HasPrefix(newStrat.ID, "strat-") {
		return nil, fmt.Errorf("supersede: agent emitted id %q without strat- prefix", newStrat.ID)
	}

	plan, err := cascade.ComputeSupersedePlan(loaded, id, newStrat.ID, eventID)
	if err != nil {
		return nil, fmt.Errorf("supersede: compute plan: %w", err)
	}
	historian := history.NewHistorian(fsys, ".borg/history")
	evt, err := cascade.ApplySupersedeStrategy(fsys, plan, newStrat, motivation, justifySession, historian)
	if err != nil {
		return nil, fmt.Errorf("supersede: apply: %w", err)
	}
	return supersedeRefineResult(plan, evt, result.Rationale, motivation), nil
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
