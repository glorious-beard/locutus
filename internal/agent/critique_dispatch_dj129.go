// DJ-129 dimension-driven critique dispatch.
//
// The scout enumerates CritiqueDimensions in its brief; this file's
// helpers convert them into per-dimension fanout items the workflow
// dispatches against, plus the stability check that gates convergence
// on monotonic-add (new-dimension introduction blocks; retirement
// does not — per design decision #7).

package agent

import (
	"fmt"
	"strings"
)

// CritiqueDimensionItem is one fanout item the critique step
// dispatches against. AgentID is always spec_critic_elaborator (the
// parametric critic). ID is "crit:<dimension.id>" so fanoutItemID
// returns a unique label per dispatch slot. Dimension carries the
// full CritiqueDimension for the projection to render in the user
// message.
type CritiqueDimensionItem struct {
	AgentID   string            `json:"agent_id"`
	ID        string            `json:"id"`
	Dimension CritiqueDimension `json:"dimension"`
}

// fanoutCritiqueDimensions returns one JSON-marshaled fanout item per
// dimension in state.CurrentCritiqueDimensions. Empty input returns
// an empty slice (the workflow skips the critique step naturally when
// the scout surfaces no dimensions).
func fanoutCritiqueDimensions(s *PlanningState) ([]string, error) {
	if s == nil || len(s.CurrentCritiqueDimensions) == 0 {
		return nil, nil
	}
	items := make([]any, 0, len(s.CurrentCritiqueDimensions))
	for _, d := range s.CurrentCritiqueDimensions {
		items = append(items, CritiqueDimensionItem{
			AgentID:   "spec_critic_elaborator",
			ID:        fmt.Sprintf("crit:%s", d.ID),
			Dimension: d,
		})
	}
	return marshalFanoutItems(items)
}

// recordDimensionStability walks the iteration's surfaced dimensions
// and records each id's first-seen iteration in
// s.CritiqueDimensionsByIter. Append-only — existing entries are
// preserved so a retired-and-recurring dimension keeps its original
// first-seen iter (design decision #7: retirement is a positive
// signal, not a reset).
//
// Lazily initialises the map. Called from mergeScoutBrief after the
// brief is parsed.
func recordDimensionStability(s *PlanningState, current []CritiqueDimension, iter int) {
	if s == nil || len(current) == 0 {
		return
	}
	if s.CritiqueDimensionsByIter == nil {
		s.CritiqueDimensionsByIter = make(map[string]int, len(current))
	}
	for _, d := range current {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		if _, seen := s.CritiqueDimensionsByIter[id]; !seen {
			s.CritiqueDimensionsByIter[id] = iter
		}
	}
}

// dimensionsAreStable returns true when every dimension in the
// current iteration's set has already been recorded in
// s.CritiqueDimensionsByIter — i.e., the scout surfaced no NEW
// dimensions this turn. Retirement (current set is a subset of the
// recorded set) returns true; new-addition returns false.
//
// Empty current set is trivially stable. The convergence rule in
// scoutSpawnFor gates exit on (Converged AND dimensionsAreStable).
func dimensionsAreStable(s *PlanningState) bool {
	if s == nil || len(s.CurrentCritiqueDimensions) == 0 {
		return true
	}
	for _, d := range s.CurrentCritiqueDimensions {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		if _, recorded := s.CritiqueDimensionsByIter[id]; !recorded {
			return false
		}
	}
	return true
}
