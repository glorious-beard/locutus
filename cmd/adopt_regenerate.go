package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// regenerateInvalidatedApproaches finds Approaches with
// InvalidatedByEventID populated and rewrites their Body via the
// approach-regenerator agent. The structured fields (ArtifactPaths,
// Decisions, Assertions, Skills, Prerequisites) carry forward — the
// cascade engine already rewrote any id references the supersede
// affected, so only the Body is stale. On success the approach's
// InvalidatedByEventID is cleared and UpdatedAt is refreshed.
//
// Called from RunAdoptWithConfig's Phase 0, alongside
// synthesizeMissingApproaches. Returns the list of regenerated
// approach ids for inclusion in AdoptReport.
//
// Soft-degrades when the supersede event referenced by an approach
// is missing from disk: logs a warning and skips. Tighter coupling
// between approach state and on-disk event integrity would run
// counter to the two-way-door DX the verb set is organised around.
func regenerateInvalidatedApproaches(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS) ([]string, error) {
	if llm == nil {
		return nil, nil
	}
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, fmt.Errorf("regenerate: load spec: %w", err)
	}

	invalidated := invalidatedApproaches(loaded)
	if len(invalidated) == 0 {
		return nil, nil
	}

	def, err := scaffold.LoadAgent(fsys, "approach-regenerator")
	if err != nil {
		return nil, fmt.Errorf("regenerate: load agent: %w", err)
	}

	regenerated := make([]string, 0, len(invalidated))
	for _, app := range invalidated {
		evt, ok, err := readSupersedeEvent(fsys, app.InvalidatedByEventID)
		if err != nil {
			return regenerated, fmt.Errorf("regenerate: read event for %s: %w", app.ID, err)
		}
		if !ok {
			slog.Warn("regenerate: skipping approach with missing supersede event",
				"approach", app.ID, "event_id", app.InvalidatedByEventID)
			continue
		}

		rctx, err := buildRegenerateContext(loaded, app, evt)
		if err != nil {
			slog.Warn("regenerate: skipping approach with missing parent context",
				"approach", app.ID, "error", err)
			continue
		}

		result, err := agent.InvokeApproachRegenerator(ctx, agent.NewDispatcher(llm), def, rctx)
		if err != nil {
			return regenerated, fmt.Errorf("regenerate %s: %w", app.ID, err)
		}

		app.Body = result.RevisedBody
		app.InvalidatedByEventID = ""
		app.UpdatedAt = time.Now().UTC()

		fp := path.Join(".borg/spec/approaches", app.ID+".md")
		if err := specio.SaveMarkdown(fsys, fp, app, result.RevisedBody); err != nil {
			return regenerated, fmt.Errorf("regenerate: persist %s: %w", app.ID, err)
		}
		regenerated = append(regenerated, app.ID)
	}
	return regenerated, nil
}

// invalidatedApproaches returns Approaches with non-empty
// InvalidatedByEventID, sorted by id for deterministic ordering
// across runs.
func invalidatedApproaches(loaded *spec.Loaded) []spec.Approach {
	var out []spec.Approach
	for _, n := range loaded.Approaches {
		if n.Spec.IsInvalidated() {
			out = append(out, n.Spec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// readSupersedeEvent loads `.borg/history/<event-id>.json`. Returns
// ok=false (no error) when the file doesn't exist — the caller
// degrades to a skip + warning, since a missing event file is a
// FAFO scenario the spec graph shouldn't fail wholesale on.
func readSupersedeEvent(fsys specio.FS, eventID string) (history.Event, bool, error) {
	if eventID == "" {
		return history.Event{}, false, nil
	}
	data, err := fsys.ReadFile(path.Join(".borg/history", eventID+".json"))
	if err != nil {
		return history.Event{}, false, nil
	}
	var evt history.Event
	if err := json.Unmarshal(data, &evt); err != nil {
		return history.Event{}, false, fmt.Errorf("unmarshal event %s: %w", eventID, err)
	}
	return evt, true, nil
}

// buildRegenerateContext assembles the agent-input payload from the
// loaded graph and the supersede event. Returns an error when the
// approach's parent can't be located in loaded — which means the
// graph is inconsistent (parent file missing) and adopt should skip
// rather than fabricate a context.
func buildRegenerateContext(loaded *spec.Loaded, app spec.Approach, evt history.Event) (agent.RegenerateApproachContext, error) {
	rctx := agent.RegenerateApproachContext{
		PriorApproach: app,
	}

	if f := loaded.FeatureNodeByID(app.ParentID); f != nil {
		rctx.ParentKind = spec.KindFeature
		rctx.ParentID = f.Spec.ID
		rctx.ParentTitle = f.Spec.Title
		rctx.ParentProse = f.Spec.Description
		rctx.CurrentDecisions = collectDecisionsFromLoaded(loaded, f.Spec.Decisions)
	} else if s := loaded.StrategyNodeByID(app.ParentID); s != nil {
		rctx.ParentKind = spec.KindStrategy
		rctx.ParentID = s.Spec.ID
		rctx.ParentTitle = s.Spec.Title
		rctx.ParentProse = s.Body
		rctx.CurrentDecisions = collectDecisionsFromLoaded(loaded, s.Spec.Decisions)
	} else if b := loaded.BugNodeByID(app.ParentID); b != nil {
		rctx.ParentKind = spec.KindBug
		rctx.ParentID = b.Spec.ID
		rctx.ParentTitle = b.Spec.Title
		rctx.ParentProse = b.Spec.Description
		// Bugs inherit decisions from their parent feature.
		if pf := loaded.FeatureNodeByID(b.Spec.FeatureID); pf != nil {
			rctx.CurrentDecisions = collectDecisionsFromLoaded(loaded, pf.Spec.Decisions)
		}
	} else {
		return rctx, fmt.Errorf("parent %q not found for approach %q", app.ParentID, app.ID)
	}

	if evt.Supersede != nil {
		rctx.SupersedeMotivation = evt.Supersede.Motivation
	}
	rctx.SupersededID = evt.TargetID
	rctx.ReplacementID = evt.NewValue

	return rctx, nil
}

// collectDecisionsFromLoaded returns the Decision structs for the given ids in
// order, skipping any id whose Decision was deleted from the graph
// (e.g. the supersede cascade rewrote a reference but the rewriting
// raced with another mutation — the slice still contains the live
// decisions).
func collectDecisionsFromLoaded(loaded *spec.Loaded, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d := loaded.DecisionNodeByID(id); d != nil {
			out = append(out, d.Spec)
		}
	}
	return out
}
