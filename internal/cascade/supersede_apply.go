package cascade

import (
	"fmt"
	"path"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ApplySupersedeDecision executes a decision-target supersede plan
// against fsys. Writes the new decision, deletes the old (when not
// in-place), rewrites cascade buckets, invalidates approaches, and
// records the history event. Returns the recorded event so callers
// can surface the cascade scope to the user.
//
// motivation flows into the history event's Supersede.Motivation
// field. justifySession is the optional pointer at the .locutus/
// sessions/.../session.yaml that produced the breaking-point
// analysis (DJ-085 non-load-bearing pointer); pass empty string when
// supersede was invoked without a preceding justify run.
func ApplySupersedeDecision(fsys specio.FS, plan *SupersedePlan, newDec spec.Decision, motivation, justifySession string, historian *history.Historian) (*history.Event, error) {
	if plan.NodeKind != spec.KindDecision {
		return nil, fmt.Errorf("apply supersede: plan kind is %q, not decision", plan.NodeKind)
	}
	if newDec.ID != plan.NewID {
		return nil, fmt.Errorf("apply supersede: new decision id %q does not match plan NewID %q", newDec.ID, plan.NewID)
	}

	if !plan.InPlace {
		if err := rewriteFeatureDecisionRefs(fsys, plan); err != nil {
			return nil, err
		}
		if err := rewriteStrategyDecisionRefs(fsys, plan); err != nil {
			return nil, err
		}
		if err := rewriteDecisionInfluencedByRefs(fsys, plan); err != nil {
			return nil, err
		}
	}
	if err := invalidateApproachesForDecision(fsys, plan); err != nil {
		return nil, err
	}
	if err := writeNewDecision(fsys, plan, newDec); err != nil {
		return nil, err
	}
	return recordSupersedeEvent(historian, plan, motivation, justifySession)
}

// ApplySupersedeFeature executes a feature-target supersede plan.
func ApplySupersedeFeature(fsys specio.FS, plan *SupersedePlan, newFeat spec.Feature, motivation, justifySession string, historian *history.Historian) (*history.Event, error) {
	if plan.NodeKind != spec.KindFeature {
		return nil, fmt.Errorf("apply supersede: plan kind is %q, not feature", plan.NodeKind)
	}
	if newFeat.ID != plan.NewID {
		return nil, fmt.Errorf("apply supersede: new feature id %q does not match plan NewID %q", newFeat.ID, plan.NewID)
	}

	if !plan.InPlace {
		if err := rewriteBugFeatureRefs(fsys, plan); err != nil {
			return nil, err
		}
	}
	if err := invalidateApproachesForFeature(fsys, plan); err != nil {
		return nil, err
	}
	if err := writeNewFeature(fsys, plan, newFeat); err != nil {
		return nil, err
	}
	return recordSupersedeEvent(historian, plan, motivation, justifySession)
}

// ApplySupersedeStrategy executes a strategy-target supersede plan.
func ApplySupersedeStrategy(fsys specio.FS, plan *SupersedePlan, newStrat spec.Strategy, motivation, justifySession string, historian *history.Historian) (*history.Event, error) {
	if plan.NodeKind != spec.KindStrategy {
		return nil, fmt.Errorf("apply supersede: plan kind is %q, not strategy", plan.NodeKind)
	}
	if newStrat.ID != plan.NewID {
		return nil, fmt.Errorf("apply supersede: new strategy id %q does not match plan NewID %q", newStrat.ID, plan.NewID)
	}

	if err := invalidateApproachesForStrategy(fsys, plan); err != nil {
		return nil, err
	}
	if err := writeNewStrategy(fsys, plan, newStrat); err != nil {
		return nil, err
	}
	return recordSupersedeEvent(historian, plan, motivation, justifySession)
}

// --- new-node writes (incl. delete-old when not in-place) ---

func writeNewDecision(fsys specio.FS, plan *SupersedePlan, newDec spec.Decision) error {
	newPath := path.Join(".borg/spec/decisions", plan.NewID)
	if err := specio.SavePair(fsys, newPath, newDec, ""); err != nil {
		return fmt.Errorf("write new decision %q: %w", plan.NewID, err)
	}
	if !plan.InPlace {
		if err := removePair(fsys, ".borg/spec/decisions", plan.OldID); err != nil {
			return fmt.Errorf("remove old decision %q: %w", plan.OldID, err)
		}
	}
	return nil
}

func writeNewFeature(fsys specio.FS, plan *SupersedePlan, newFeat spec.Feature) error {
	newPath := path.Join(".borg/spec/features", plan.NewID)
	if err := specio.SavePair(fsys, newPath, newFeat, ""); err != nil {
		return fmt.Errorf("write new feature %q: %w", plan.NewID, err)
	}
	if !plan.InPlace {
		if err := removePair(fsys, ".borg/spec/features", plan.OldID); err != nil {
			return fmt.Errorf("remove old feature %q: %w", plan.OldID, err)
		}
	}
	return nil
}

func writeNewStrategy(fsys specio.FS, plan *SupersedePlan, newStrat spec.Strategy) error {
	newPath := path.Join(".borg/spec/strategies", plan.NewID)
	if err := specio.SavePair(fsys, newPath, newStrat, ""); err != nil {
		return fmt.Errorf("write new strategy %q: %w", plan.NewID, err)
	}
	if !plan.InPlace {
		if err := removePair(fsys, ".borg/spec/strategies", plan.OldID); err != nil {
			return fmt.Errorf("remove old strategy %q: %w", plan.OldID, err)
		}
	}
	return nil
}

// removePair removes both the .json and .md sidecars for a node id.
// Missing files are not errors — the cascade may have already moved
// them, or a caller may invoke this for a half-state cleanup.
func removePair(fsys specio.FS, dir, id string) error {
	for _, suffix := range []string{".json", ".md"} {
		fp := path.Join(dir, id+suffix)
		if err := fsys.Remove(fp); err != nil && !isNotExist(err) {
			return err
		}
	}
	return nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	// specio FS errors compose os.ErrNotExist via standard wrappers;
	// the substring fallback covers MemFS implementations that may
	// return their own typed errors.
	msg := err.Error()
	return msg != "" && (containsAny(msg, "no such file", "does not exist", "not found"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// --- id-rewrite cascades ---

func rewriteFeatureDecisionRefs(fsys specio.FS, plan *SupersedePlan) error {
	for _, fid := range plan.FeaturesToRewrite {
		basePath := path.Join(".borg/spec/features", fid)
		feat, body, err := specio.LoadPair[spec.Feature](fsys, basePath)
		if err != nil {
			return fmt.Errorf("load feature %q: %w", fid, err)
		}
		feat.Decisions = replaceFirst(feat.Decisions, plan.OldID, plan.NewID)
		feat.UpdatedAt = time.Now().UTC()
		if err := specio.SavePair(fsys, basePath, feat, body); err != nil {
			return fmt.Errorf("save feature %q: %w", fid, err)
		}
	}
	return nil
}

func rewriteStrategyDecisionRefs(fsys specio.FS, plan *SupersedePlan) error {
	for _, sid := range plan.StrategiesToRewrite {
		basePath := path.Join(".borg/spec/strategies", sid)
		s, body, err := specio.LoadPair[spec.Strategy](fsys, basePath)
		if err != nil {
			return fmt.Errorf("load strategy %q: %w", sid, err)
		}
		s.Decisions = replaceFirst(s.Decisions, plan.OldID, plan.NewID)
		if err := specio.SavePair(fsys, basePath, s, body); err != nil {
			return fmt.Errorf("save strategy %q: %w", sid, err)
		}
	}
	return nil
}

func rewriteDecisionInfluencedByRefs(fsys specio.FS, plan *SupersedePlan) error {
	for _, did := range plan.DecisionsInfluencedByToRewrite {
		basePath := path.Join(".borg/spec/decisions", did)
		dec, body, err := specio.LoadPair[spec.Decision](fsys, basePath)
		if err != nil {
			return fmt.Errorf("load decision %q: %w", did, err)
		}
		dec.InfluencedBy = replaceFirst(dec.InfluencedBy, plan.OldID, plan.NewID)
		dec.UpdatedAt = time.Now().UTC()
		if err := specio.SavePair(fsys, basePath, dec, body); err != nil {
			return fmt.Errorf("save decision %q: %w", did, err)
		}
	}
	return nil
}

func rewriteBugFeatureRefs(fsys specio.FS, plan *SupersedePlan) error {
	for _, bid := range plan.BugsToRewrite {
		basePath := path.Join(".borg/spec/bugs", bid)
		b, body, err := specio.LoadPair[spec.Bug](fsys, basePath)
		if err != nil {
			return fmt.Errorf("load bug %q: %w", bid, err)
		}
		if b.FeatureID == plan.OldID {
			b.FeatureID = plan.NewID
		}
		b.UpdatedAt = time.Now().UTC()
		if err := specio.SavePair(fsys, basePath, b, body); err != nil {
			return fmt.Errorf("save bug %q: %w", bid, err)
		}
	}
	return nil
}

// --- approach invalidation ---

func invalidateApproachesForDecision(fsys specio.FS, plan *SupersedePlan) error {
	for _, aid := range plan.ApproachesToInvalidate {
		fp := path.Join(".borg/spec/approaches", aid+".md")
		a, body, err := specio.LoadMarkdown[spec.Approach](fsys, fp)
		if err != nil {
			return fmt.Errorf("load approach %q: %w", aid, err)
		}
		a.InvalidatedByEventID = plan.EventID
		if !plan.InPlace {
			a.Decisions = replaceFirst(a.Decisions, plan.OldID, plan.NewID)
		}
		a.UpdatedAt = time.Now().UTC()
		if err := specio.SaveMarkdown(fsys, fp, a, body); err != nil {
			return fmt.Errorf("save approach %q: %w", aid, err)
		}
	}
	return nil
}

func invalidateApproachesForFeature(fsys specio.FS, plan *SupersedePlan) error {
	for _, aid := range plan.ApproachesToInvalidate {
		fp := path.Join(".borg/spec/approaches", aid+".md")
		a, body, err := specio.LoadMarkdown[spec.Approach](fsys, fp)
		if err != nil {
			return fmt.Errorf("load approach %q: %w", aid, err)
		}
		a.InvalidatedByEventID = plan.EventID
		if !plan.InPlace && a.ParentID == plan.OldID {
			a.ParentID = plan.NewID
		}
		a.UpdatedAt = time.Now().UTC()
		if err := specio.SaveMarkdown(fsys, fp, a, body); err != nil {
			return fmt.Errorf("save approach %q: %w", aid, err)
		}
	}
	return nil
}

func invalidateApproachesForStrategy(fsys specio.FS, plan *SupersedePlan) error {
	// Same shape as feature — strategies and features both use
	// Approach.ParentID. Kept as separate function for clarity in the
	// caller and so future strategy-specific behaviour can diverge
	// without unwinding a shared path.
	return invalidateApproachesForFeature(fsys, plan)
}

// replaceFirst returns a copy of ids with the first occurrence of
// oldID replaced by newID. Order is preserved. If oldID does not
// appear, the slice is returned unchanged (caller's invariant).
func replaceFirst(ids []string, oldID, newID string) []string {
	for i, id := range ids {
		if id == oldID {
			out := make([]string, len(ids))
			copy(out, ids)
			out[i] = newID
			return out
		}
	}
	return ids
}

// --- history ---

func recordSupersedeEvent(historian *history.Historian, plan *SupersedePlan, motivation, justifySession string) (*history.Event, error) {
	evt := history.Event{
		ID:        plan.EventID,
		Timestamp: time.Now().UTC(),
		Kind:      history.EventKindNodeSuperseded,
		TargetID:  plan.OldID,
		NewValue:  plan.NewID,
		Supersede: &history.SupersedeRecord{
			NodeKind:                       string(plan.NodeKind),
			InPlace:                        plan.InPlace,
			Motivation:                     motivation,
			JustifySession:                 justifySession,
			FeaturesDecisionsRewritten:     plan.FeaturesToRewrite,
			StrategiesDecisionsRewritten:   plan.StrategiesToRewrite,
			DecisionsInfluencedByRewritten: plan.DecisionsInfluencedByToRewrite,
			BugsFeatureIDRewritten:         plan.BugsToRewrite,
			ApproachesInvalidated:          plan.ApproachesToInvalidate,
		},
	}
	if err := historian.Record(evt); err != nil {
		return nil, fmt.Errorf("record supersede event: %w", err)
	}
	return &evt, nil
}
