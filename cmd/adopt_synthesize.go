package cmd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// synthesizeMissingApproaches walks the spec graph for features and
// strategies that have no Approach children and asks the synthesizer
// LLM to produce one per parent. New Approaches are persisted as
// `app-<parent-id>.md` and the parent's `approaches[]` slice is updated
// on disk so the next reconcile.Classify sees them as `unplanned`.
//
// Per the council-resilience plan: approaches are synthesized at adopt
// time when real code context exists, not invented during refine when
// it doesn't. Returns the list of synthesized Approach IDs.
//
// Scope, when non-empty, restricts synthesis to parents reachable from
// the scope node (matching the scope filter applied to classification).
//
// Dry-run is handled by the caller wrapping fsys with readOnlyFS — the
// LLM call still happens and the synthesized Approach is reported, but
// the writes are silently dropped.
func synthesizeMissingApproaches(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	graph *spec.SpecGraph,
	scope string,
) ([]string, error) {
	if llm == nil {
		return nil, nil
	}

	parents := parentsMissingApproaches(fsys, graph, scope)
	if len(parents) == 0 {
		return nil, nil
	}

	synthesized := make([]string, 0, len(parents))
	for _, p := range parents {
		approachID := "app-" + p.ID
		applicable := applicableDecisionsFor(graph, p.Decisions)

		approach := spec.Approach{
			ID:        approachID,
			Title:     p.Title,
			ParentID:  p.ID,
			Decisions: p.Decisions,
		}
		resp, err := invokeSynthesizer(ctx, llm, approach, p, applicable)
		if err != nil {
			return synthesized, fmt.Errorf("synthesize approach for %s: %w", p.ID, err)
		}
		approach.Body = resp.RevisedBody
		approach.CreatedAt = time.Now()
		approach.UpdatedAt = approach.CreatedAt

		if err := specio.SaveMarkdown(fsys, ".borg/spec/approaches/"+approachID+".md", approach, resp.RevisedBody); err != nil {
			return synthesized, fmt.Errorf("persist approach %s: %w", approachID, err)
		}

		if err := attachApproachToParent(fsys, p, approachID); err != nil {
			return synthesized, fmt.Errorf("attach %s to %s: %w", approachID, p.ID, err)
		}

		synthesized = append(synthesized, approachID)
	}
	return synthesized, nil
}

// parentsMissingApproaches returns features and strategies in scope that
// have no entries in their Approaches slice. Caller treats each as a
// candidate for on-demand approach synthesis. Sorted by ID for
// deterministic ordering across runs.
func parentsMissingApproaches(fsys specio.FS, graph *spec.SpecGraph, scope string) []parentContext {
	inScope := scopeFilter(graph, scope)
	var out []parentContext
	for id, n := range graph.Nodes() {
		if !inScope(id) {
			continue
		}
		switch n.Kind {
		case spec.KindFeature:
			f := graph.Feature(id)
			if f == nil || len(f.Approaches) > 0 {
				continue
			}
			out = append(out, parentContext{
				Kind: spec.KindFeature, ID: f.ID, Title: f.Title,
				Prose: f.Description, Decisions: f.Decisions,
			})
		case spec.KindStrategy:
			s := graph.Strategy(id)
			if s == nil || len(s.Approaches) > 0 {
				continue
			}
			body, _ := fsys.ReadFile(".borg/spec/strategies/" + s.ID + ".md")
			out = append(out, parentContext{
				Kind: spec.KindStrategy, ID: s.ID, Title: s.Title,
				Prose: string(body), Decisions: s.Decisions,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// scopeFilter returns a predicate that admits ids in the scope subtree.
// An empty scope (or RootID) admits every id.
func scopeFilter(graph *spec.SpecGraph, scope string) func(string) bool {
	if scope == "" || scope == spec.RootID {
		return func(string) bool { return true }
	}
	in := make(map[string]struct{})
	for _, n := range graph.ForwardWalk(scope) {
		in[n.ID] = struct{}{}
	}
	return func(id string) bool {
		_, ok := in[id]
		return ok
	}
}

// attachApproachToParent appends the new approach ID to the parent's
// Approaches slice and persists the parent back to disk. Both Feature
// and Strategy persist as a JSON+md pair (LoadPair/SavePair); the .md
// body is preserved verbatim during the read-modify-write.
func attachApproachToParent(fsys specio.FS, p parentContext, approachID string) error {
	switch p.Kind {
	case spec.KindFeature:
		f, body, err := specio.LoadPair[spec.Feature](fsys, ".borg/spec/features/"+p.ID)
		if err != nil {
			return fmt.Errorf("load feature: %w", err)
		}
		if containsString(f.Approaches, approachID) {
			return nil
		}
		f.Approaches = append(f.Approaches, approachID)
		f.UpdatedAt = time.Now()
		if err := specio.SavePair(fsys, ".borg/spec/features/"+p.ID, f, body); err != nil {
			return fmt.Errorf("save feature: %w", err)
		}
	case spec.KindStrategy:
		s, body, err := specio.LoadPair[spec.Strategy](fsys, ".borg/spec/strategies/"+p.ID)
		if err != nil {
			return fmt.Errorf("load strategy: %w", err)
		}
		if containsString(s.Approaches, approachID) {
			return nil
		}
		s.Approaches = append(s.Approaches, approachID)
		if err := specio.SavePair(fsys, ".borg/spec/strategies/"+p.ID, s, body); err != nil {
			return fmt.Errorf("save strategy: %w", err)
		}
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
