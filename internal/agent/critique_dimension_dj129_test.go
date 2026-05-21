package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCritiqueDimensionRoundTrip verifies marshal/unmarshal preserves
// every field including the bounded discipline enum slice.
func TestCritiqueDimensionRoundTrip(t *testing.T) {
	in := CritiqueDimension{
		ID:             "cost-ceiling-coverage",
		Lens:           "cost",
		FocusQuestion:  "Does every paid commitment engage with GOALS §3's $150/mo ceiling?",
		SourceEvidence: []string{"GOALS §3", "dec-datadog rationale"},
		Disciplines:    []string{"web_grounded", "goals_grounded"},
		SeverityFloor:  "high",
	}
	out, err := json.Marshal(in)
	require.NoError(t, err)
	var loaded CritiqueDimension
	require.NoError(t, json.Unmarshal(out, &loaded))
	assert.Equal(t, in, loaded)
}

// TestScoutBriefRoundTripsCritiqueDimensions verifies the new
// CritiqueDimensions field marshals/unmarshals as part of ScoutBrief.
func TestScoutBriefRoundTripsCritiqueDimensions(t *testing.T) {
	in := ScoutBrief{
		DomainRead: "test",
		CritiqueDimensions: []CritiqueDimension{{
			ID:             "cost-ceiling-coverage",
			Lens:           "cost",
			FocusQuestion:  "Does every commitment fit the cost ceiling?",
			SourceEvidence: []string{"GOALS §3"},
			Disciplines:    []string{"web_grounded", "goals_grounded"},
			SeverityFloor:  "high",
		}},
	}
	out, err := json.Marshal(in)
	require.NoError(t, err)
	var loaded ScoutBrief
	require.NoError(t, json.Unmarshal(out, &loaded))
	require.Len(t, loaded.CritiqueDimensions, 1)
	assert.Equal(t, in.CritiqueDimensions[0], loaded.CritiqueDimensions[0])
}

// TestPlanningStateCarriesDimensionFields verifies the new fields
// exist and deep-copy correctly through snapshotPlanningState.
func TestPlanningStateCarriesDimensionFields(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{"cost-ceiling-coverage": 2},
		CurrentCritiqueDimensions: []CritiqueDimension{{
			ID: "cost-ceiling-coverage", Lens: "cost",
			FocusQuestion: "q", SourceEvidence: []string{"e"},
			Disciplines: []string{"web_grounded"}, SeverityFloor: "high",
		}},
	}
	snap := snapshotPlanningState(s)
	require.Len(t, snap.CurrentCritiqueDimensions, 1)
	require.Equal(t, 2, snap.CritiqueDimensionsByIter["cost-ceiling-coverage"])

	// Mutating the snapshot must not affect the original.
	snap.CritiqueDimensionsByIter["x"] = 99
	assert.NotContains(t, s.CritiqueDimensionsByIter, "x", "snapshot must deep-copy the map")
}

// TestCritiqueDimensionSchemaRegistered verifies SchemaFor returns
// a non-nil schema for CritiqueDimension and the bounded discipline
// enum is preserved.
func TestCritiqueDimensionSchemaRegistered(t *testing.T) {
	schema, err := SchemaFor("CritiqueDimension")
	require.NoError(t, err)
	require.NotNil(t, schema)
	out, err := json.Marshal(schema)
	require.NoError(t, err)
	body := string(out)
	for _, v := range []string{"web_grounded", "spec_node_grounded", "best_practice_grounded", "goals_grounded", "freeform"} {
		assert.Contains(t, body, v, "discipline enum value %q must be in schema", v)
	}
}
