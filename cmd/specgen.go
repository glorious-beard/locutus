package cmd

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/chetan/locutus/internal/agent"
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
func runSpecGeneration(ctx context.Context, llm agent.LLM, fsys specio.FS, req agent.SpecGenRequest) (*GenerationSummary, error) {
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

	return &GenerationSummary{
		Features:   len(proposal.Features),
		Decisions:  len(proposal.Decisions),
		Strategies: len(proposal.Strategies),
		Approaches: len(proposal.Approaches),
	}, nil
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
