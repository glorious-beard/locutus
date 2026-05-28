// Package migrate carries one-shot on-disk migrations of the Locutus
// spec graph. Currently houses the DJ-133 decision-ID migration: every
// persisted `dec-<chosen-option>` is renamed to `dec-<primary-axis>` so
// the decision ID becomes the question (the axis) rather than the
// answer. Idempotent: a second pass against a fully migrated graph is a
// no-op.
package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
)

// EventKindDecisionIDMigration is the DJ-133 per-decision rename event.
// TargetID is the old id; NewValue is the new id; Rationale enumerates
// the refs the migration rewrote alongside the rename.
const EventKindDecisionIDMigration = "decision_id_migration"

// DecisionIDMigrationResult summarises a migration pass. Populated even
// on partial failure so the operator's surface can show the work that
// did land.
type DecisionIDMigrationResult struct {
	Renamed []DecisionRename
	Skipped []DecisionSkip
	Events  []history.Event
}

// DecisionRename describes one rename and the refs it touched. The
// Composite flag is set when the source decision carried multiple axes;
// the operator-visible log warns on these so the secondary axes don't
// silently disappear from the id.
type DecisionRename struct {
	OldID                  string
	NewID                  string
	Axes                   []string
	Composite              bool
	FeaturesRewritten      []string
	StrategiesRewritten    []string
	DecisionsInfluencedBy  []string
	ApproachesRewritten    []string
}

// DecisionSkip records a decision the migration left alone with the
// reason ("already-axis-shaped" / "empty-axes").
type DecisionSkip struct {
	ID     string
	Reason string
}

// MigrateDecisionIDs renames every persisted decision whose id is
// `dec-<chosen-option>` to `dec-<primary-axis>`, rewriting all incoming
// references in features, strategies, decisions (influenced_by), and
// approaches. Conflicts (two source decisions mapping to the same
// target) are detected and reported before any rename happens, so
// partial state is impossible. Each rename writes one
// decision_id_migration history event when historian is non-nil.
//
// Returns the migration's summary. A nil error with empty Renamed
// means the graph was already fully axis-shaped — the natural state on
// every call after the first.
func MigrateDecisionIDs(fsys specio.FS, historian *history.Historian) (*DecisionIDMigrationResult, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}

	result := &DecisionIDMigrationResult{}

	// Pass 1: build the rename map and bucket skips. Walk decisions in
	// id order so the operator-visible log is deterministic.
	type plannedRename struct {
		OldID, NewID string
		Axes         []string
		Composite    bool
	}
	var planned []plannedRename
	renameByOld := make(map[string]string)
	collisionsByNew := make(map[string][]string)

	decisions := append([]spec.DecisionNode(nil), loaded.Decisions...)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].Spec.ID < decisions[j].Spec.ID
	})

	for _, dn := range decisions {
		d := dn.Spec
		if dn.LoadErr != nil {
			result.Skipped = append(result.Skipped, DecisionSkip{
				ID:     d.ID,
				Reason: fmt.Sprintf("load-error: %v", dn.LoadErr),
			})
			continue
		}
		if len(d.Axes) == 0 {
			result.Skipped = append(result.Skipped, DecisionSkip{
				ID:     d.ID,
				Reason: "empty-axes",
			})
			continue
		}
		primary := strings.TrimSpace(d.Axes[0])
		if primary == "" {
			result.Skipped = append(result.Skipped, DecisionSkip{
				ID:     d.ID,
				Reason: "empty-axes",
			})
			continue
		}
		newID := "dec-" + primary
		if newID == d.ID {
			result.Skipped = append(result.Skipped, DecisionSkip{
				ID:     d.ID,
				Reason: "already-axis-shaped",
			})
			continue
		}
		planned = append(planned, plannedRename{
			OldID:     d.ID,
			NewID:     newID,
			Axes:      append([]string(nil), d.Axes...),
			Composite: len(d.Axes) > 1,
		})
		renameByOld[d.ID] = newID
		collisionsByNew[newID] = append(collisionsByNew[newID], d.ID)
	}

	// Conflict check: two distinct olds mapping to the same new id is a
	// hard error — the operator hand-resolves before re-running. Order
	// the diagnostic message so reruns produce the same output.
	var conflicts []string
	for newID, sources := range collisionsByNew {
		if len(sources) > 1 {
			sort.Strings(sources)
			conflicts = append(conflicts, fmt.Sprintf("%s ⇐ %s", newID, strings.Join(sources, ", ")))
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return result, fmt.Errorf(
			"decision-id migration: %d target id(s) have multiple source decisions — operator must hand-resolve before retry:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "),
		)
	}

	if len(planned) == 0 {
		return result, nil
	}

	// Pass 2: execute renames + ref rewrites. Each iteration is
	// self-contained — the file rename + ref rewrites for one decision
	// happen together, then the history event records the per-decision
	// scope. If a single iteration fails partway, the result records
	// what landed; the error surfaces to the caller.
	for _, p := range planned {
		rename := DecisionRename{
			OldID:     p.OldID,
			NewID:     p.NewID,
			Axes:      p.Axes,
			Composite: p.Composite,
		}

		// (a) Rewrite the decision file: load old, change id, save at
		// new path, delete old path.
		dn := loaded.DecisionNodeByID(p.OldID)
		if dn == nil {
			return result, fmt.Errorf("decision-id migration: decision %q vanished mid-pass", p.OldID)
		}
		newDec := dn.Spec
		newDec.ID = p.NewID
		newDec.UpdatedAt = time.Now().UTC()
		if err := specio.SavePair(fsys, path.Join(".borg/spec/decisions", p.NewID), newDec, dn.Body); err != nil {
			return result, fmt.Errorf("decision-id migration: write new decision %q: %w", p.NewID, err)
		}
		if err := removePair(fsys, ".borg/spec/decisions", p.OldID); err != nil {
			return result, fmt.Errorf("decision-id migration: remove old decision %q: %w", p.OldID, err)
		}

		// (b) Rewrite feature.Decisions[] entries.
		for _, fid := range loaded.FeaturesReferencingDecision(p.OldID) {
			basePath := path.Join(".borg/spec/features", fid)
			feat, body, err := specio.LoadPair[spec.Feature](fsys, basePath)
			if err != nil {
				return result, fmt.Errorf("decision-id migration: load feature %q: %w", fid, err)
			}
			feat.Decisions = replaceAll(feat.Decisions, p.OldID, p.NewID)
			feat.UpdatedAt = time.Now().UTC()
			if err := specio.SavePair(fsys, basePath, feat, body); err != nil {
				return result, fmt.Errorf("decision-id migration: save feature %q: %w", fid, err)
			}
			rename.FeaturesRewritten = append(rename.FeaturesRewritten, fid)
		}

		// (c) Rewrite strategy.Decisions[] entries.
		for _, sid := range loaded.StrategiesReferencingDecision(p.OldID) {
			basePath := path.Join(".borg/spec/strategies", sid)
			s, body, err := specio.LoadPair[spec.Strategy](fsys, basePath)
			if err != nil {
				return result, fmt.Errorf("decision-id migration: load strategy %q: %w", sid, err)
			}
			s.Decisions = replaceAll(s.Decisions, p.OldID, p.NewID)
			if err := specio.SavePair(fsys, basePath, s, body); err != nil {
				return result, fmt.Errorf("decision-id migration: save strategy %q: %w", sid, err)
			}
			rename.StrategiesRewritten = append(rename.StrategiesRewritten, sid)
		}

		// (d) Rewrite decision.InfluencedBy[] entries on sibling
		// decisions. The target decision itself may carry the old id
		// in someone else's InfluencedBy slice; rewriting only the old
		// ↦ new entry keeps everything else intact.
		for _, did := range loaded.DecisionsInfluencedByDecision(p.OldID) {
			basePath := path.Join(".borg/spec/decisions", did)
			dec, body, err := specio.LoadPair[spec.Decision](fsys, basePath)
			if err != nil {
				return result, fmt.Errorf("decision-id migration: load decision %q: %w", did, err)
			}
			dec.InfluencedBy = replaceAll(dec.InfluencedBy, p.OldID, p.NewID)
			dec.UpdatedAt = time.Now().UTC()
			if err := specio.SavePair(fsys, basePath, dec, body); err != nil {
				return result, fmt.Errorf("decision-id migration: save decision %q: %w", did, err)
			}
			rename.DecisionsInfluencedBy = append(rename.DecisionsInfluencedBy, did)
		}

		// (e) Rewrite approach.Decisions[] entries. Approaches don't
		// have a Loaded inverse-index helper, so walk every approach.
		// Approach bodies stay byte-stable — the id-only migration does
		// not invalidate the synthesised brief.
		for _, an := range loaded.Approaches {
			if !contains(an.Spec.Decisions, p.OldID) {
				continue
			}
			fp := path.Join(".borg/spec/approaches", an.Spec.ID+".md")
			a, body, err := specio.LoadMarkdown[spec.Approach](fsys, fp)
			if err != nil {
				return result, fmt.Errorf("decision-id migration: load approach %q: %w", an.Spec.ID, err)
			}
			a.Decisions = replaceAll(a.Decisions, p.OldID, p.NewID)
			a.UpdatedAt = time.Now().UTC()
			if err := specio.SaveMarkdown(fsys, fp, a, body); err != nil {
				return result, fmt.Errorf("decision-id migration: save approach %q: %w", an.Spec.ID, err)
			}
			rename.ApproachesRewritten = append(rename.ApproachesRewritten, an.Spec.ID)
		}

		result.Renamed = append(result.Renamed, rename)

		// (f) Record the per-decision history event.
		if historian != nil {
			evt := buildDecisionIDMigrationEvent(rename)
			if err := historian.Record(evt); err != nil {
				return result, fmt.Errorf("decision-id migration: record event for %q: %w", p.OldID, err)
			}
			result.Events = append(result.Events, evt)
		}
	}

	return result, nil
}

// buildDecisionIDMigrationEvent renders a per-decision history event.
// TargetID is the old id; NewValue is the new id; Rationale enumerates
// the affected refs so `locutus history` carries a self-contained
// record of what the migration touched.
func buildDecisionIDMigrationEvent(r DecisionRename) history.Event {
	now := time.Now().UTC()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "Decision id migrated from %s to %s under DJ-133 (decisions identified by axis).", r.OldID, r.NewID)
	if r.Composite {
		fmt.Fprintf(&rationale, "\n\nNote: source decision carried %d axes (%s); the migration uses the primary axis %q. Secondary axes remain in axes[] but the id no longer reflects them.",
			len(r.Axes), strings.Join(r.Axes, ", "), r.Axes[0])
	}
	if n := len(r.FeaturesRewritten); n > 0 {
		fmt.Fprintf(&rationale, "\n\nFeatures rewritten (%d):", n)
		for _, id := range r.FeaturesRewritten {
			fmt.Fprintf(&rationale, "\n- %s", id)
		}
	}
	if n := len(r.StrategiesRewritten); n > 0 {
		fmt.Fprintf(&rationale, "\n\nStrategies rewritten (%d):", n)
		for _, id := range r.StrategiesRewritten {
			fmt.Fprintf(&rationale, "\n- %s", id)
		}
	}
	if n := len(r.DecisionsInfluencedBy); n > 0 {
		fmt.Fprintf(&rationale, "\n\nDecisions (influenced_by) rewritten (%d):", n)
		for _, id := range r.DecisionsInfluencedBy {
			fmt.Fprintf(&rationale, "\n- %s", id)
		}
	}
	if n := len(r.ApproachesRewritten); n > 0 {
		fmt.Fprintf(&rationale, "\n\nApproaches rewritten (%d):", n)
		for _, id := range r.ApproachesRewritten {
			fmt.Fprintf(&rationale, "\n- %s", id)
		}
	}
	return history.Event{
		ID:        history.EventID(EventKindDecisionIDMigration, r.OldID, now),
		Timestamp: now,
		Kind:      EventKindDecisionIDMigration,
		TargetID:  r.OldID,
		NewValue:  r.NewID,
		Rationale: rationale.String(),
	}
}

// removePair removes a node's .json + .md sidecars. Missing files are
// not errors — matches the cascade-package convention.
func removePair(fsys specio.FS, dir, id string) error {
	for _, suffix := range []string{".json", ".md"} {
		fp := path.Join(dir, id+suffix)
		if err := fsys.Remove(fp); err != nil && !isNotExist(err) {
			return err
		}
	}
	return nil
}

// isNotExist matches the not-exist sentinel from both OSFS and MemFS,
// both of which wrap their errors as *fs.PathError with fs.ErrNotExist
// as the underlying cause. Required on Windows, where the OS error
// message ("The system cannot find the file specified") does not
// contain the Unix-style substrings the original string-match version
// looked for.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// replaceAll returns a copy of ids with every occurrence of oldID
// replaced by newID. Order is preserved. Unlike cascade.replaceFirst,
// this rewrites every entry — a single decision id can appear at most
// once per decisions[] in well-formed data, but defending against the
// duplicate case keeps the migration deterministic when an upstream
// dispatch left a stray duplicate.
func replaceAll(ids []string, oldID, newID string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, len(ids))
	copy(out, ids)
	mutated := false
	for i, id := range out {
		if id == oldID {
			out[i] = newID
			mutated = true
		}
	}
	if !mutated {
		return ids
	}
	return out
}

func contains(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}
