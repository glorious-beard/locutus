package agent

import (
	"sort"

	"github.com/chetan/locutus/internal/spec"
)

// SpecChangeKind enumerates the four states a node can be in when
// comparing two ExistingSpec snapshots: present in both unchanged
// (Stable), present in both with changed content (Modified),
// present only in the after snapshot (Added), or present only in
// the before snapshot (Abandoned).
type SpecChangeKind string

const (
	SpecChangeStable    SpecChangeKind = "stable"
	SpecChangeModified  SpecChangeKind = "modified"
	SpecChangeAdded     SpecChangeKind = "added"
	SpecChangeAbandoned SpecChangeKind = "abandoned"
)

// SpecChange names one node and its category in a diff. Used by
// the operator-facing summary and (planned) by the abandoned-ID
// cleanup pass that follows.
type SpecChange struct {
	Kind  SpecChangeKind
	Type  string // "feature" | "decision" | "strategy" | "approach"
	ID    string
	Title string
}

// SpecDiff aggregates per-node changes between two ExistingSpec
// snapshots. Each slice is sorted by (Type, ID) so output is
// deterministic across re-runs even when the underlying snapshots
// reorder.
type SpecDiff struct {
	Added     []SpecChange
	Modified  []SpecChange
	Abandoned []SpecChange
	Stable    []SpecChange
}

// Counts returns (added, modified, abandoned, stable) cardinalities
// in a single call so the operator-facing summary can assemble the
// one-line "5 added, 3 modified, 2 abandoned, 45 stable" line
// without re-iterating the slices.
func (d SpecDiff) Counts() (added, modified, abandoned, stable int) {
	return len(d.Added), len(d.Modified), len(d.Abandoned), len(d.Stable)
}

// ComputeSpecDiff diffs two ExistingSpec snapshots — typically the
// pre-run state (loaded at refine start) and the post-run state
// (reloaded after persistence). Categorizes each ID into Added /
// Modified / Abandoned / Stable so an operator can see exactly
// what a refine pass changed without resorting to git diff.
//
// "Modified" is determined by content comparison (title +
// kind-specific load-bearing field — Description for features,
// Rationale for decisions, etc.); generated timestamps and status
// fields are intentionally NOT part of the comparison so a
// re-persist that touches only those fields registers as Stable.
func ComputeSpecDiff(before, after *ExistingSpec) SpecDiff {
	if before == nil {
		before = &ExistingSpec{}
	}
	if after == nil {
		after = &ExistingSpec{}
	}

	var diff SpecDiff
	diffFeatures(&diff, before.Features, after.Features)
	diffDecisions(&diff, before.Decisions, after.Decisions)
	diffStrategies(&diff, before.Strategies, after.Strategies)
	diffApproaches(&diff, before.Approaches, after.Approaches)

	sortChanges(diff.Added)
	sortChanges(diff.Modified)
	sortChanges(diff.Abandoned)
	sortChanges(diff.Stable)
	return diff
}

func diffFeatures(diff *SpecDiff, before, after []spec.Feature) {
	beforeIdx := indexFeatures(before)
	afterIdx := indexFeatures(after)
	for id, a := range afterIdx {
		b, found := beforeIdx[id]
		change := SpecChange{Type: "feature", ID: id, Title: a.Title}
		if !found {
			change.Kind = SpecChangeAdded
			diff.Added = append(diff.Added, change)
			continue
		}
		if featureContentEqual(b, a) {
			change.Kind = SpecChangeStable
			diff.Stable = append(diff.Stable, change)
		} else {
			change.Kind = SpecChangeModified
			diff.Modified = append(diff.Modified, change)
		}
	}
	for id, b := range beforeIdx {
		if _, found := afterIdx[id]; !found {
			diff.Abandoned = append(diff.Abandoned, SpecChange{
				Kind: SpecChangeAbandoned, Type: "feature", ID: id, Title: b.Title,
			})
		}
	}
}

func diffDecisions(diff *SpecDiff, before, after []spec.Decision) {
	beforeIdx := indexDecisions(before)
	afterIdx := indexDecisions(after)
	for id, a := range afterIdx {
		b, found := beforeIdx[id]
		change := SpecChange{Type: "decision", ID: id, Title: a.Title}
		if !found {
			change.Kind = SpecChangeAdded
			diff.Added = append(diff.Added, change)
			continue
		}
		if decisionContentEqual(b, a) {
			change.Kind = SpecChangeStable
			diff.Stable = append(diff.Stable, change)
		} else {
			change.Kind = SpecChangeModified
			diff.Modified = append(diff.Modified, change)
		}
	}
	for id, b := range beforeIdx {
		if _, found := afterIdx[id]; !found {
			diff.Abandoned = append(diff.Abandoned, SpecChange{
				Kind: SpecChangeAbandoned, Type: "decision", ID: id, Title: b.Title,
			})
		}
	}
}

func diffStrategies(diff *SpecDiff, before, after []spec.Strategy) {
	beforeIdx := indexStrategies(before)
	afterIdx := indexStrategies(after)
	for id, a := range afterIdx {
		b, found := beforeIdx[id]
		change := SpecChange{Type: "strategy", ID: id, Title: a.Title}
		if !found {
			change.Kind = SpecChangeAdded
			diff.Added = append(diff.Added, change)
			continue
		}
		if strategyContentEqual(b, a) {
			change.Kind = SpecChangeStable
			diff.Stable = append(diff.Stable, change)
		} else {
			change.Kind = SpecChangeModified
			diff.Modified = append(diff.Modified, change)
		}
	}
	for id, b := range beforeIdx {
		if _, found := afterIdx[id]; !found {
			diff.Abandoned = append(diff.Abandoned, SpecChange{
				Kind: SpecChangeAbandoned, Type: "strategy", ID: id, Title: b.Title,
			})
		}
	}
}

func diffApproaches(diff *SpecDiff, before, after []spec.Approach) {
	beforeIdx := indexApproaches(before)
	afterIdx := indexApproaches(after)
	for id, a := range afterIdx {
		b, found := beforeIdx[id]
		change := SpecChange{Type: "approach", ID: id, Title: a.Title}
		if !found {
			change.Kind = SpecChangeAdded
			diff.Added = append(diff.Added, change)
			continue
		}
		if approachContentEqual(b, a) {
			change.Kind = SpecChangeStable
			diff.Stable = append(diff.Stable, change)
		} else {
			change.Kind = SpecChangeModified
			diff.Modified = append(diff.Modified, change)
		}
	}
	for id, b := range beforeIdx {
		if _, found := afterIdx[id]; !found {
			diff.Abandoned = append(diff.Abandoned, SpecChange{
				Kind: SpecChangeAbandoned, Type: "approach", ID: id, Title: b.Title,
			})
		}
	}
}

// Content-equality predicates. Compare load-bearing fields only;
// generated timestamps (CreatedAt, UpdatedAt, GeneratedAt) and
// lifecycle status are intentionally excluded so a no-content
// re-persist registers as Stable rather than Modified.

func featureContentEqual(a, b spec.Feature) bool {
	return a.Title == b.Title && a.Description == b.Description
}

func decisionContentEqual(a, b spec.Decision) bool {
	return a.Title == b.Title && a.Rationale == b.Rationale
}

func strategyContentEqual(a, b spec.Strategy) bool {
	return a.Title == b.Title && a.Kind == b.Kind
}

func approachContentEqual(a, b spec.Approach) bool {
	return a.Title == b.Title && a.Body == b.Body
}

func indexFeatures(items []spec.Feature) map[string]spec.Feature {
	out := make(map[string]spec.Feature, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func indexDecisions(items []spec.Decision) map[string]spec.Decision {
	out := make(map[string]spec.Decision, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func indexStrategies(items []spec.Strategy) map[string]spec.Strategy {
	out := make(map[string]spec.Strategy, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func indexApproaches(items []spec.Approach) map[string]spec.Approach {
	out := make(map[string]spec.Approach, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func sortChanges(changes []SpecChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Type != changes[j].Type {
			return changes[i].Type < changes[j].Type
		}
		return changes[i].ID < changes[j].ID
	})
}
