package cascade

import (
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// SupersedePlan describes everything a supersede operation will touch
// before any writes happen. ComputeSupersedePlan produces it from a
// loaded spec graph; ApplySupersedePlan executes it. Splitting the
// two lets `--dry-run` preview cascade scope without committing.
//
// The InPlace bool is the same-slug shortcut: when the new node's
// title slugifies to the existing id, no cross-node id rewrites are
// needed. Approaches still get invalidated because the node's
// content changed materially even though its id didn't.
type SupersedePlan struct {
	NodeKind spec.NodeKind
	OldID    string
	NewID    string
	InPlace  bool
	EventID  string

	FeaturesToRewrite              []string
	StrategiesToRewrite            []string
	DecisionsInfluencedByToRewrite []string
	BugsToRewrite                  []string
	ApproachesToInvalidate         []string
}

// ComputeSupersedePlan walks the loaded graph and determines which
// nodes need to change for a supersession of oldID by newID. The
// returned plan is purely informational — no files are touched.
//
// EventID is required (caller-supplied so the same event id flows
// through to the history record and the InvalidatedByEventID field
// on every affected approach).
func ComputeSupersedePlan(loaded *spec.Loaded, oldID, newID, eventID string) (*SupersedePlan, error) {
	if strings.TrimSpace(eventID) == "" {
		return nil, fmt.Errorf("supersede plan: event id is required")
	}
	kind, err := nodeKindForID(loaded, oldID)
	if err != nil {
		return nil, err
	}
	if kind == spec.KindBug {
		return nil, fmt.Errorf("supersede plan: %q is a bug — bugs use status transitions, not supersede; see refine --brief", oldID)
	}

	plan := &SupersedePlan{
		NodeKind: kind,
		OldID:    oldID,
		NewID:    newID,
		InPlace:  oldID == newID,
		EventID:  eventID,
	}

	switch kind {
	case spec.KindDecision:
		populateDecisionCascade(loaded, plan)
	case spec.KindFeature:
		populateFeatureCascade(loaded, plan)
	case spec.KindStrategy:
		populateStrategyCascade(loaded, plan)
	}
	return plan, nil
}

// nodeKindForID locates the node by id and returns its kind. Returns
// an error for unknown ids and for ids whose prefix is not a known
// node kind (the prefix-only check is a sanity gate; presence in
// loaded is the actual existence check).
func nodeKindForID(loaded *spec.Loaded, id string) (spec.NodeKind, error) {
	switch {
	case strings.HasPrefix(id, "dec-"):
		if loaded.DecisionNodeByID(id) == nil {
			return "", fmt.Errorf("supersede plan: decision %q not found", id)
		}
		return spec.KindDecision, nil
	case strings.HasPrefix(id, "feat-"):
		if loaded.FeatureNodeByID(id) == nil {
			return "", fmt.Errorf("supersede plan: feature %q not found", id)
		}
		return spec.KindFeature, nil
	case strings.HasPrefix(id, "strat-"):
		if loaded.StrategyNodeByID(id) == nil {
			return "", fmt.Errorf("supersede plan: strategy %q not found", id)
		}
		return spec.KindStrategy, nil
	case strings.HasPrefix(id, "bug-"):
		if loaded.BugNodeByID(id) == nil {
			return "", fmt.Errorf("supersede plan: bug %q not found", id)
		}
		return spec.KindBug, nil
	}
	return "", fmt.Errorf("supersede plan: unknown id prefix in %q", id)
}

// populateDecisionCascade fills the cascade buckets that apply when a
// decision is the supersede target. In-place revisions skip the id
// rewrite buckets (no id change) but still invalidate approaches
// because the decision's structured fields changed materially.
func populateDecisionCascade(loaded *spec.Loaded, plan *SupersedePlan) {
	if !plan.InPlace {
		plan.FeaturesToRewrite = loaded.FeaturesReferencingDecision(plan.OldID)
		plan.StrategiesToRewrite = loaded.StrategiesReferencingDecision(plan.OldID)
		plan.DecisionsInfluencedByToRewrite = loaded.DecisionsInfluencedByDecision(plan.OldID)
	}
	for _, a := range loaded.Approaches {
		for _, did := range a.Spec.Decisions {
			if did == plan.OldID {
				plan.ApproachesToInvalidate = append(plan.ApproachesToInvalidate, a.Spec.ID)
				break
			}
		}
	}
}

// populateFeatureCascade fills the cascade buckets that apply when a
// feature is the supersede target. Bugs filed against the feature get
// FeatureID rewritten; approaches under the feature get invalidated.
func populateFeatureCascade(loaded *spec.Loaded, plan *SupersedePlan) {
	if !plan.InPlace {
		for _, b := range loaded.Bugs {
			if b.Spec.FeatureID == plan.OldID {
				plan.BugsToRewrite = append(plan.BugsToRewrite, b.Spec.ID)
			}
		}
	}
	for _, a := range loaded.Approaches {
		if a.Spec.ParentID == plan.OldID {
			plan.ApproachesToInvalidate = append(plan.ApproachesToInvalidate, a.Spec.ID)
		}
	}
}

// populateStrategyCascade fills the cascade buckets that apply when a
// strategy is the supersede target. Strategies are stand-alone in the
// typed model — only approaches under the strategy are affected.
func populateStrategyCascade(loaded *spec.Loaded, plan *SupersedePlan) {
	for _, a := range loaded.Approaches {
		if a.Spec.ParentID == plan.OldID {
			plan.ApproachesToInvalidate = append(plan.ApproachesToInvalidate, a.Spec.ID)
		}
	}
}
