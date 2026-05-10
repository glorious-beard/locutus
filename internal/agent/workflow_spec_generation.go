package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// SpecGenerationWorkflow drives `locutus refine goals` and `locutus
// import <doc>`'s post-admission planning pass.
//
// DJ-098 unified per-cluster revise. Critic findings route through:
//
//  1. survey      — senior-engineer scout brief.
//  2. outline     — feature/strategy titles + summaries.
//  3. elaborate_features  — fanout: one elaborator per outlined feature.
//  4. elaborate_strategies — fanout: one elaborator per outlined strategy.
//  5. reconcile   — clusters inline decisions across the assembled
//     proposal; emits canonical SpecProposal.
//  6. critique    — four LLM critics + the in-workflow integrity
//     critic, parallel.
//  7. cluster_findings — LLM clusterer groups any critic findings the
//     mechanical pre-pass couldn't id-match. Conditional on
//     hasUnmatchedFindings.
//  8. revise      — fanout (one call per FindingCluster). Each cluster
//     carries its own agent_id (set by the mechanical pre-pass from
//     id prefix or by the LLM clusterer's kind). Conditional on
//     hasFindingClusters.
//  9. reconcile_revise — same reconciler against the merged raw proposal
//     (original + revised + added). Conditional on hasFindingClusters.
//
// The mechanical pre-pass that populates state.FindingClusters from
// state.Concerns runs inside mergeCriticIssues — no explicit step. It
// groups findings that name an existing node id; the LLM clusterer
// step processes the rest.
//
// MaxRounds=1 because there is no convergence agent in this workflow.
//
// Per-model concurrency caps live in models.yaml's `concurrent_requests`
// field. Even with Parallel=true on fanout steps, the actual
// concurrency is bounded so fanout never floods a model past its
// configured slot count.
var SpecGenerationWorkflow = &Workflow[PlanningState]{
	Snapshot:       snapshotPlanningState,
	DefaultProject: projectDefault,
	Rounds: []WorkflowStep[PlanningState]{
		{
			ID:      "survey",
			Agents:  []string{"spec_scout"},
			Project: projectDefault,
			Merge:   mergeScoutBrief,
		},
		{
			ID:        "outline",
			Agents:    []string{"spec_outliner"},
			DependsOn: []string{"survey"},
			Project:   projectOutline,
			Merge:     mergeOutline,
		},
		{
			ID:        "elaborate_features",
			Agents:    []string{"spec_feature_elaborator"},
			Parallel:  true,
			DependsOn: []string{"outline"},
			Fanout:    fanoutOutlineFeatures,
			Project:   projectElaborateFeature,
			Merge:     mergeElaboratedFeatures,
		},
		{
			ID:        "elaborate_strategies",
			Agents:    []string{"spec_strategy_elaborator"},
			Parallel:  true,
			DependsOn: []string{"outline"},
			Fanout:    fanoutOutlineStrategies,
			Project:   projectElaborateStrategy,
			Merge:     mergeElaboratedStrategies,
		},
		{
			ID:        "reconcile",
			Agents:    []string{"spec_reconciler"},
			DependsOn: []string{"elaborate_features", "elaborate_strategies"},
			Project:   projectReconcile,
			Merge:     mergeReconciledProposal,
		},
		{
			ID:        "critique",
			Agents:    []string{"architect_critic", "devops_critic", "sre_critic", "cost_critic"},
			Parallel:  true,
			DependsOn: []string{"reconcile"},
			Project:   projectChallenge,
			Merge:     mergeCriticIssues,
		},
		{
			ID:          "cluster_findings",
			Agents:      []string{"spec_finding_clusterer"},
			DependsOn:   []string{"critique"},
			Conditional: hasUnmatchedFindings,
			Project:     projectClusterFindings,
			Merge:       mergeFindingClusters,
		},
		{
			// Step-level agent is the fallback when a cluster doesn't
			// carry its own agent_id (shouldn't happen — both the
			// mechanical pre-pass and the LLM-cluster promotion always
			// set one — but keeping this as a safety net).
			ID:          "revise",
			Agents:      []string{"spec_strategy_elaborator"},
			Parallel:    true,
			DependsOn:   []string{"cluster_findings"},
			Conditional: hasFindingClusters,
			Fanout:      fanoutFindingClusters,
			Project:     projectFindingCluster,
			Merge:       mergeRevisedNodes,
		},
		{
			ID:          "reconcile_revise",
			Agents:      []string{"spec_reconciler"},
			DependsOn:   []string{"revise"},
			Conditional: hasFindingClusters,
			Project:     projectReconcile,
			Merge:       mergeReconciledProposal,
		},
	},
	MaxRounds: 1,
}

// hasUnmatchedFindings (DJ-098) gates the LLM clusterer step. True when
// the mechanical pre-pass left critic findings that name no existing
// node id. False when every finding has already been routed to a
// per-node cluster.
func hasUnmatchedFindings(s *PlanningState) bool { return len(s.UnmatchedFindings) > 0 }

// hasFindingClusters (DJ-098) gates the revise fanout and
// reconcile_revise. True when at least one FindingCluster (mechanical
// or LLM) is present. Skipped on runs where critics produced no
// findings.
func hasFindingClusters(s *PlanningState) bool { return len(s.FindingClusters) > 0 }

// fanoutOutlineFeatures returns one raw-JSON OutlineFeature per outlined
// feature. Each entry drives a per-element spec_feature_elaborator call.
func fanoutOutlineFeatures(state *PlanningState) ([]string, error) {
	if state == nil || state.Outline == "" {
		return nil, nil
	}
	var outline Outline
	if err := json.Unmarshal([]byte(state.Outline), &outline); err != nil {
		return nil, fmt.Errorf("parse outline: %w", err)
	}
	items := make([]any, 0, len(outline.Features))
	for _, f := range outline.Features {
		items = append(items, f)
	}
	return marshalFanoutItems(items)
}

// fanoutOutlineStrategies is the strategy counterpart to
// fanoutOutlineFeatures.
func fanoutOutlineStrategies(state *PlanningState) ([]string, error) {
	if state == nil || state.Outline == "" {
		return nil, nil
	}
	var outline Outline
	if err := json.Unmarshal([]byte(state.Outline), &outline); err != nil {
		return nil, fmt.Errorf("parse outline: %w", err)
	}
	items := make([]any, 0, len(outline.Strategies))
	for _, s := range outline.Strategies {
		items = append(items, s)
	}
	return marshalFanoutItems(items)
}

// fanoutFindingClusters (DJ-098 unified revise) returns one raw-JSON
// FindingCluster per non-empty cluster. AgentID is sniffed off each item
// by ExecuteRound so the dispatcher routes to the right elaborator per
// cluster. Empty-findings clusters are dropped here so the downstream
// dispatch never sees them.
func fanoutFindingClusters(state *PlanningState) ([]string, error) {
	if state == nil {
		return nil, nil
	}
	items := make([]any, 0, len(state.FindingClusters))
	for _, c := range state.FindingClusters {
		if len(c.Findings) == 0 {
			slog.Warn("fanout: dropping FindingCluster with no findings", "topic", c.Topic)
			continue
		}
		items = append(items, c)
	}
	return marshalFanoutItems(items)
}

// mergeScoutBrief stores the spec_scout's structured ScoutBrief output.
func mergeScoutBrief(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.ScoutBrief = v
	}
}

// mergeOutline stores the spec_outliner's Outline JSON. Stashed for the
// downstream fanout (fanoutOutlineFeatures/Strategies reads it) and for
// each elaborator's projection (sibling situational awareness).
func mergeOutline(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.Outline = v
	}
}

// mergeElaboratedFeatures appends each fanout call's output to
// ElaboratedFeatures and re-runs the assembler. Idempotent and order-
// independent — both elaborate-fanout merges converge on the same
// RawProposal as soon as the data is available.
func mergeElaboratedFeatures(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.ElaboratedFeatures = append(s.ElaboratedFeatures, r.Output)
	}
	if assembled, ok := assembleRawProposal(s); ok {
		s.RawProposal = assembled
		s.OriginalRawProposal = assembled
	}
}

// mergeElaboratedStrategies is the strategy counterpart.
func mergeElaboratedStrategies(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.ElaboratedStrategies = append(s.ElaboratedStrategies, r.Output)
	}
	if assembled, ok := assembleRawProposal(s); ok {
		s.RawProposal = assembled
		s.OriginalRawProposal = assembled
	}
}

// mergeReconciledProposal turns the reconciler's verdict into the
// canonical SpecProposal via ApplyReconciliation. Errors are recorded as
// integrity-kind Concerns so revise can surface them; the workflow
// itself does not fail.
func mergeReconciledProposal(s *PlanningState, results []RoundResult) {
	verdict := firstNonEmpty(results)
	if verdict == "" {
		return
	}
	canonical, applied, err := mergeReconcile(s.RawProposal, verdict, s.Existing)
	if err != nil {
		// First non-empty result's agent is the reconciler; surface
		// per-result errors via the reconcile-attribution path.
		for _, r := range results {
			if r.Err == nil && r.Output != "" {
				s.Concerns = append(s.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "high",
					Text:     fmt.Sprintf("reconcile: %s", err.Error()),
				})
				return
			}
		}
		return
	}
	s.ProposedSpec = canonical
	s.ConflictActions = appendConflictActions(s.ConflictActions, applied)
}

// mergeCriticIssues parses each critic's CriticIssues output into
// per-issue Concerns tagged with the critic's lens (architecture,
// devops, sre, cost) for grouping in the revise prompt. After the LLM
// critics merge, runs the mechanical integrity critic and the
// mechanical cluster pre-pass (DJ-098).
func mergeCriticIssues(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		kind := critiqueKindFor(r.AgentID)
		var ci CriticIssues
		if err := json.Unmarshal([]byte(r.Output), &ci); err != nil {
			s.Concerns = append(s.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Kind:     kind,
				Text:     r.Output,
			})
			continue
		}
		for _, issue := range ci.Issues {
			s.Concerns = append(s.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Kind:     kind,
				Text:     issue,
			})
		}
	}
	appendIntegrityFindings(s)
	runMechanicalCluster(s)
}

// mergeFindingClusters (DJ-098) promotes the LLM clusterer's output into
// FindingCluster entries with AgentID set from kind, appending to the
// mechanical pre-pass output.
func mergeFindingClusters(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.FindingClusters = append(s.FindingClusters, PromoteLLMClusters(v)...)
	}
}

// mergeRevisedNodes (DJ-098) accumulates per-cluster elaborator outputs
// and rebuilds RawProposal. Each entry is one RawFeatureProposal or
// RawStrategyProposal; the assembler sniffs id prefix and decides
// revise (id matches existing) vs add (fresh id) per entry.
// Idempotent.
func mergeRevisedNodes(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.RevisedNodes = append(s.RevisedNodes, r.Output)
	}
	if merged, ok := assembleRevisedRawProposal(s); ok {
		s.RawProposal = merged
	}
}
