package history

import (
	"time"
)

// EventKindBiased is the canonical kind for the root event of a
// `locutus refine --with "<bias>"` cascade run (DJ-138). One
// spec_biased event lands per dispatch; every downstream
// spec_revised / spec_proposed / approach_drifted event recorded
// during the same cascade carries CausedBy set to the
// spec_biased event's id so `locutus history --since <bias-id>`
// can walk the full cascade subtree.
const EventKindBiased = "spec_biased"

// EventKindApproachDrifted records that an approach was marked
// stale via spec_mark_approach_drifted during a cascade. The
// approach's InvalidatedByEventID field also stores the
// originating bias event id; this event is the auditable
// counterpart on the history log.
const EventKindApproachDrifted = "approach_drifted"

// BiasedRecord is the structured payload for spec_biased events.
// Captured at dispatch time before the cascade enters its first
// iteration so even a totally failed cascade leaves an auditable
// root.
type BiasedRecord struct {
	BiasText       string             `json:"bias_text"`
	BlastRadius    BlastRadiusEstimate `json:"blast_radius_estimate"`
	ACPSessionID   string             `json:"acp_session_id,omitempty"`
}

// BlastRadiusEstimate is the pre-cascade closure-walk estimate
// printed to the operator before the dispatch banner. The cascade
// may exceed these counts when it adds new citations or new
// decisions, but the estimate captures the deliberate scope at
// dispatch time.
type BlastRadiusEstimate struct {
	DecisionsTouched     int `json:"decisions_touched"`
	FeaturesRewritten    int `json:"features_rewritten"`
	StrategiesRewritten  int `json:"strategies_rewritten"`
	ApproachesDrifted    int `json:"approaches_drifted"`
}

// ApproachDriftedRecord is the structured payload for
// approach_drifted events. UpstreamEventID points at the
// spec_revised event whose write caused the closure walk to
// drift this approach — different from CausedBy on the parent
// Event, which always points at the spec_biased root.
type ApproachDriftedRecord struct {
	UpstreamEventID string `json:"upstream_event_id,omitempty"`
}

// RecordBiased writes the spec_biased root event of a `--with`
// cascade. Called by the CLI verb at dispatch time, before any
// playbook iteration runs.
func RecordBiased(h *Historian, targetID, biasText, acpSessionID string, blastRadius BlastRadiusEstimate) (string, error) {
	now := time.Now()
	id := EventID("biased", targetID, now)
	return id, h.Record(Event{
		ID:        id,
		Timestamp: now,
		Kind:      EventKindBiased,
		TargetID:  targetID,
		Rationale: biasText,
		Biased: &BiasedRecord{
			BiasText:     biasText,
			BlastRadius:  blastRadius,
			ACPSessionID: acpSessionID,
		},
	})
}

// RecordApproachDrifted writes one approach_drifted event for a
// single approach marked during a cascade. causedBy is the
// originating spec_biased event id; upstreamEventID is the
// spec_revised event whose write triggered the drift.
func RecordApproachDrifted(h *Historian, approachID, causedBy, upstreamEventID string) (string, error) {
	now := time.Now()
	id := EventID("approach-drifted", approachID, now)
	return id, h.Record(Event{
		ID:        id,
		Timestamp: now,
		Kind:      EventKindApproachDrifted,
		TargetID:  approachID,
		CausedBy:  causedBy,
		ApproachDrifted: &ApproachDriftedRecord{
			UpstreamEventID: upstreamEventID,
		},
	})
}

// EventsCausedBy walks the history log and returns every event
// whose CausedBy points at the given root event id (typically a
// spec_biased event). Used by `locutus history --since
// <bias-event-id>` and by status renderers that want to surface
// the cascade subtree of a recent bias.
func (h *Historian) EventsCausedBy(rootID string) ([]Event, error) {
	all, err := h.Events()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range all {
		if e.CausedBy == rootID {
			out = append(out, e)
		}
	}
	return out, nil
}

// RecentBiased returns the n most-recent spec_biased events,
// newest first. Used by `locutus status --full` to surface
// recent cascades. Returns all events when fewer than n exist;
// returns nil when none exist.
func (h *Historian) RecentBiased(n int) ([]Event, error) {
	all, err := h.Events()
	if err != nil {
		return nil, err
	}
	var biased []Event
	for _, e := range all {
		if e.Kind == EventKindBiased {
			biased = append(biased, e)
		}
	}
	if len(biased) == 0 {
		return nil, nil
	}
	// h.Events() returns timestamp-ascending; reverse to newest-first.
	for i, j := 0, len(biased)-1; i < j; i, j = i+1, j-1 {
		biased[i], biased[j] = biased[j], biased[i]
	}
	if n > 0 && len(biased) > n {
		biased = biased[:n]
	}
	return biased, nil
}
