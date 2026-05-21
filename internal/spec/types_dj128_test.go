// DJ-128 — schema tests for Alternative (deliberation-log fields) and
// Decision (Locked cap-as-commit flag).

package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlternativeCarriesIterationAndConcernText — a RejectedAtIteration
// + RejectedByConcernText round-trip through marshal/unmarshal.
func TestAlternativeCarriesIterationAndConcernText(t *testing.T) {
	alt := Alternative{
		Name:                  "MySQL",
		Rationale:             "Familiar to the team and a common default at this scale.",
		RejectedBecause:       "JSONB-equivalent storage is bolted on rather than first-class.",
		Citations:             []Citation{{Kind: "web", Reference: "https://dev.mysql.com/doc/", Excerpt: "JSON values are stored as native JSON."}},
		RejectedAtIteration:   3,
		RejectedByConcernText: "dec-postgres-oltp-store rationale doesn't address the 50ms p99 latency budget from GOALS §3.",
	}

	out, err := json.Marshal(alt)
	require.NoError(t, err)

	var loaded Alternative
	require.NoError(t, json.Unmarshal(out, &loaded))

	assert.Equal(t, 3, loaded.RejectedAtIteration)
	assert.Equal(t, alt.RejectedByConcernText, loaded.RejectedByConcernText)
}

// TestAlternativeDeliberationFieldsOmitemptyWhenUnset — a first-author
// alternative without deliberation-log provenance marshals without
// the new fields, so legacy on-disk shapes are preserved.
func TestAlternativeDeliberationFieldsOmitemptyWhenUnset(t *testing.T) {
	alt := Alternative{
		Name:            "MySQL",
		Rationale:       "Familiar to the team.",
		RejectedBecause: "Bolted-on JSONB.",
		Citations:       []Citation{{Kind: "doc", Reference: "GOALS.md"}},
	}
	out, err := json.Marshal(alt)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "rejected_at_iteration")
	assert.NotContains(t, string(out), "rejected_by_concern_text")
}

// TestDecisionLockedFlagDefaultsFalse — legacy on-disk decisions
// without the field load with Locked: false. Round-trip preserves an
// explicit true.
func TestDecisionLockedFlagDefaultsFalse(t *testing.T) {
	legacy := `{"id":"dec-x","title":"X","status":"proposed","confidence":0.8,"rationale":"r","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`
	var d Decision
	require.NoError(t, json.Unmarshal([]byte(legacy), &d))
	assert.False(t, d.Locked)

	d.Locked = true
	out, err := json.Marshal(d)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"locked":true`)

	d.Locked = false
	out, err = json.Marshal(d)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "locked", "Locked: false must serialize via omitempty (no field in JSON)")
}
