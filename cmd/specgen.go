package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// GenerationSummary reports how many spec nodes were created or updated by
// a spec-generation pass. Used by both `refine goals` and the planning
// step inside `import` to surface what the LLM produced.
type GenerationSummary struct {
	Features         int      `json:"features"`
	Decisions        int      `json:"decisions"`
	Strategies       int      `json:"strategies"`
	Approaches       int      `json:"approaches"`
	IntegrityWarnings []string `json:"integrity_warnings,omitempty"`
}

// runSpecGeneration is the shared entry point for both `refine goals` and
// the post-admission step in `import`. It calls agent.GenerateSpec, then
// persists the resulting nodes through the same pipeline as
// `assimilate` — same atomicity, same normalization (timestamp/status
// backfill).
//
// Strategy bodies are captured separately because spec.Strategy has no
// body field; they are written into the .md sidecar via SavePair after
// the JSON has been normalised.
func runSpecGeneration(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, req agent.SpecGenRequest) (*GenerationSummary, error) {
	// Default the critic to one round when the caller hasn't been
	// explicit. The propose→critique→revise cycle catches the dangling
	// references and missing-alternative violations that the proposer
	// alone tends to slip on (especially weaker models).
	if req.CritiqueRounds == 0 {
		req.CritiqueRounds = 1
	}
	proposal, err := agent.GenerateSpec(ctx, llm, fsys, req)
	if err != nil {
		// Bubble integrity violations up to the CLI handler so it can
		// format the warning list — the message naming each dangling
		// reference is more useful than a generic "council failed".
		return nil, err
	}

	// GenerateSpec guarantees referential cleanliness; persistence
	// proceeds without further filtering. A defensive Validate here
	// would fire only on a programmer error in the integrity loop.

	result := proposal.ToAssimilationResult()
	if err := persistAssimilationResult(fsys, result); err != nil {
		return nil, err
	}
	if err := persistStrategyBodies(fsys, proposal.Strategies); err != nil {
		return nil, err
	}

	// Cascade rewrite for nodes whose decision set flipped during
	// reconciliation. The reconciler picks a winner; the architect's
	// prose was written under the loser. This pass re-aligns each
	// affected feature/strategy's body with the canonical decision.
	// Best-effort: a rewriter failure here logs and continues — the
	// proposal is already persisted under the canonical decisions.
	if err := cascadeAfterReconcile(ctx, llm, fsys, proposal); err != nil {
		slog.Warn("cascade rewrite after reconcile produced errors", "error", err)
	}

	return &GenerationSummary{
		Features:   len(proposal.Features),
		Decisions:  len(proposal.Decisions),
		Strategies: len(proposal.Strategies),
	}, nil
}

// cascadeAfterReconcile fires the rewriter for every feature/strategy
// whose decision set was flipped by the reconciler. Reads the affected
// node from disk (just persisted by persistAssimilationResult), rewrites
// the prose under the canonical decisions, saves back. Aggregates errors
// rather than aborting: a single bad rewrite shouldn't roll back the
// entire spec generation.
func cascadeAfterReconcile(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, proposal *agent.SpecProposal) error {
	if len(proposal.ConflictActions) == 0 {
		return nil
	}
	decisionByID := make(map[string]spec.Decision, len(proposal.Decisions))
	for _, d := range proposal.Decisions {
		decisionByID[d.ID] = spec.Decision{
			ID:         d.ID,
			Title:      d.Title,
			Status:     spec.DecisionStatusProposed,
			Rationale:  d.Rationale,
			Confidence: d.Confidence,
		}
	}
	featureByID := make(map[string]agent.FeatureProposal, len(proposal.Features))
	for _, f := range proposal.Features {
		featureByID[f.ID] = f
	}
	strategyByID := make(map[string]agent.StrategyProposal, len(proposal.Strategies))
	for _, s := range proposal.Strategies {
		strategyByID[s.ID] = s
	}

	// Deduplicate parents — one parent may carry multiple flipped
	// decisions; one rewrite per parent suffices.
	type parentKey struct{ kind, id string }
	seen := make(map[parentKey]struct{})
	var failures []error

	for _, action := range proposal.ConflictActions {
		for _, source := range action.AffectedNodes {
			key := parentKey{source.ParentKind, source.ParentID}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			switch source.ParentKind {
			case "feature":
				f, ok := featureByID[source.ParentID]
				if !ok {
					continue
				}
				applicable := decisionsByIDs(decisionByID, f.Decisions)
				if err := rewriteFeatureOnDisk(ctx, llm, fsys, f, applicable); err != nil {
					failures = append(failures, fmt.Errorf("feature %s: %w", f.ID, err))
				}
			case "strategy":
				s, ok := strategyByID[source.ParentID]
				if !ok {
					continue
				}
				applicable := decisionsByIDs(decisionByID, s.Decisions)
				if err := rewriteStrategyOnDisk(ctx, llm, fsys, s, applicable); err != nil {
					failures = append(failures, fmt.Errorf("strategy %s: %w", s.ID, err))
				}
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d cascade rewrite failure(s): %v", len(failures), failures)
	}
	return nil
}

// decisionsByIDs picks decisions out of the lookup map preserving the
// order in `ids`. Missing ids are silently dropped — the rewriter
// tolerates a partial decision set.
func decisionsByIDs(lookup map[string]spec.Decision, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d, ok := lookup[id]; ok {
			out = append(out, d)
		}
	}
	return out
}

// rewriteFeatureOnDisk reloads the just-persisted feature, runs the
// in-memory rewriter, and saves the new body back. Returns nil when the
// rewriter judges the prose already accurate.
func rewriteFeatureOnDisk(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, f agent.FeatureProposal, applicable []spec.Decision) error {
	persisted, _, err := specio.LoadPair[spec.Feature](fsys, ".borg/spec/features/"+f.ID)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	res, err := cascade.InvokeRewriter(ctx, llm, fsys, "feature", f.ID, f.Title, persisted.Description, applicable, nil)
	if err != nil {
		return fmt.Errorf("rewriter: %w", err)
	}
	if !res.Changed || strings.TrimSpace(res.RevisedBody) == strings.TrimSpace(persisted.Description) {
		return nil
	}
	persisted.Description = res.RevisedBody
	if err := specio.SavePair(fsys, ".borg/spec/features/"+f.ID, persisted, res.RevisedBody); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// rewriteStrategyOnDisk does the same for strategies. Strategy prose
// lives in the .md sidecar, not the typed struct, so the rewriter sees
// and replaces the .md body.
func rewriteStrategyOnDisk(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, s agent.StrategyProposal, applicable []spec.Decision) error {
	persisted, body, err := specio.LoadPair[spec.Strategy](fsys, ".borg/spec/strategies/"+s.ID)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	res, err := cascade.InvokeRewriter(ctx, llm, fsys, "strategy", s.ID, s.Title, body, applicable, nil)
	if err != nil {
		return fmt.Errorf("rewriter: %w", err)
	}
	if !res.Changed || strings.TrimSpace(res.RevisedBody) == strings.TrimSpace(body) {
		return nil
	}
	if err := specio.SavePair(fsys, ".borg/spec/strategies/"+s.ID, persisted, res.RevisedBody); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// persistStrategyBodies writes the .md sidecar for each strategy with its
// LLM-proposed body. The JSON sidecar was already written by
// persistAssimilationResult → persistStrategies (with empty body); this
// pass overwrites the .md only.
func persistStrategyBodies(fsys specio.FS, strategies []agent.StrategyProposal) error {
	if len(strategies) == 0 {
		return nil
	}
	for _, s := range strategies {
		if s.ID == "" || strings.TrimSpace(s.Body) == "" {
			continue
		}
		// Re-load the JSON written by the prior pass and re-save with body.
		var existing spec.Strategy
		jsonPath := path.Join(".borg/spec/strategies", s.ID+".json")
		data, err := fsys.ReadFile(jsonPath)
		if err != nil {
			return fmt.Errorf("strategy body persist read %s: %w", s.ID, err)
		}
		if err := mustDecodeJSON(data, &existing); err != nil {
			return fmt.Errorf("strategy body persist decode %s: %w", s.ID, err)
		}
		target := path.Join(".borg/spec/strategies", s.ID)
		if err := specio.SavePair(fsys, target, existing, s.Body); err != nil {
			return fmt.Errorf("strategy body persist save %s: %w", s.ID, err)
		}
	}
	return nil
}
