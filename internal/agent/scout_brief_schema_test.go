package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScoutBriefSchemaCarriesNewFields locks in the DJ-124 Phase 2
// shape extension: ScoutBrief gains axes_open, new_nodes, and
// converged in addition to the four legacy survey fields. Without
// the new fields reaching the strict-mode schema, the scout cannot
// signal gap state or convergence to the workflow controller — the
// loop has no exit condition and the dispatcher has no per-axis work
// list.
func TestScoutBriefSchemaCarriesNewFields(t *testing.T) {
	schema, err := SchemaFor("ScoutBrief")
	require.NoError(t, err)

	props, _ := schema["properties"].(map[string]any)
	require.NotNil(t, props, "ScoutBrief schema missing properties map")

	for _, field := range []string{"axes_open", "new_nodes", "converged"} {
		t.Run(field, func(t *testing.T) {
			node, _ := props[field].(map[string]any)
			require.NotNilf(t, node, "ScoutBrief schema must carry %q so the scout can emit DJ-124 Phase 2 gap analysis", field)
			desc, _ := node["description"].(string)
			assert.NotEmptyf(t, desc, "ScoutBrief.%s must declare a description so the model has inline guidance on what to populate", field)
		})
	}

	// Lock the top-level required list so a future refactor that
	// silences a linter by sprinkling `,omitempty` on these fields
	// doesn't quietly drop them from the strict-mode schema. The
	// workflow controller drives off all three; an absent field is
	// indistinguishable from a model that decided not to emit it.
	required, _ := schema["required"].([]any)
	requiredStrs := make([]string, 0, len(required))
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredStrs = append(requiredStrs, s)
		}
	}
	assert.Contains(t, requiredStrs, "axes_open",
		"axes_open must be required (no omitempty) — the dispatcher reads it every iteration")
	assert.Contains(t, requiredStrs, "new_nodes",
		"new_nodes must be required — the narrative-elaborator reads it every iteration")
	assert.Contains(t, requiredStrs, "converged",
		"converged must be required — the workflow controller's loop-exit gate reads it every iteration")
}

// TestScoutBriefOpenAxisRequiresSlugAndEvidence locks in the
// per-axis contract: each OpenAxis entry must carry an id, a
// description, at least one source_evidence excerpt, and at least
// one surfaced_by node reference. Without minItems on the array
// fields, a flaky model could emit an axis with empty arrays —
// the elaborator would then have no excerpts to cite back and no
// back-reference set to thread onto the eventual Decision.
func TestScoutBriefOpenAxisRequiresSlugAndEvidence(t *testing.T) {
	schema, err := SchemaFor("ScoutBrief")
	require.NoError(t, err)

	props, _ := schema["properties"].(map[string]any)
	require.NotNil(t, props, "ScoutBrief schema missing properties map")

	axesOpen, _ := props["axes_open"].(map[string]any)
	require.NotNil(t, axesOpen, "ScoutBrief schema missing axes_open property")

	items, _ := axesOpen["items"].(map[string]any)
	require.NotNil(t, items, "ScoutBrief.axes_open.items missing — array element schema not generated")

	required, _ := items["required"].([]any)
	requiredStrs := make([]string, 0, len(required))
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredStrs = append(requiredStrs, s)
		}
	}
	for _, field := range []string{"id", "description", "source_evidence", "surfaced_by"} {
		assert.Containsf(t, requiredStrs, field,
			"OpenAxis schema must mark %q as required so providers reject axes with missing fields at the API layer", field)
	}

	itemProps, _ := items["properties"].(map[string]any)
	require.NotNil(t, itemProps, "OpenAxis items missing properties map")

	for _, field := range []string{"source_evidence", "surfaced_by"} {
		t.Run(field, func(t *testing.T) {
			node, _ := itemProps[field].(map[string]any)
			require.NotNilf(t, node, "OpenAxis schema missing %s property", field)

			minItems, ok := node["minItems"]
			require.Truef(t, ok, "OpenAxis.%s must declare minItems to forbid empty arrays at the API layer", field)
			switch v := minItems.(type) {
			case int:
				assert.GreaterOrEqualf(t, v, 1, "OpenAxis.%s.minItems must be >= 1", field)
			case float64:
				assert.GreaterOrEqualf(t, v, float64(1), "OpenAxis.%s.minItems must be >= 1", field)
			default:
				t.Fatalf("OpenAxis.%s.minItems has unexpected type %T", field, minItems)
			}
		})
	}
}

// TestScoutBriefNewSpecNodeKindEnum locks in the kind discriminator
// on NewSpecNode entries: the scout emits either a feature or a
// strategy, and the strict-mode schema must reject any other value
// at the API layer. The workflow controller in Phase 5 routes
// dispatch by kind; a stray value (e.g. "decision") would either
// crash the controller or silently drop the new node.
func TestScoutBriefNewSpecNodeKindEnum(t *testing.T) {
	schema, err := SchemaFor("ScoutBrief")
	require.NoError(t, err)

	props, _ := schema["properties"].(map[string]any)
	require.NotNil(t, props, "ScoutBrief schema missing properties map")

	newNodes, _ := props["new_nodes"].(map[string]any)
	require.NotNil(t, newNodes, "ScoutBrief schema missing new_nodes property")

	items, _ := newNodes["items"].(map[string]any)
	require.NotNil(t, items, "ScoutBrief.new_nodes.items missing — array element schema not generated")

	itemProps, _ := items["properties"].(map[string]any)
	require.NotNil(t, itemProps, "NewSpecNode items missing properties map")

	kind, _ := itemProps["kind"].(map[string]any)
	require.NotNil(t, kind, "NewSpecNode schema missing kind property")

	enum, _ := kind["enum"].([]any)
	require.NotEmpty(t, enum, "NewSpecNode.kind must declare an enum so providers reject stray values at the API layer")

	enumStrs := make([]string, 0, len(enum))
	for _, e := range enum {
		if s, ok := e.(string); ok {
			enumStrs = append(enumStrs, s)
		}
	}
	assert.ElementsMatch(t, []string{"feature", "strategy"}, enumStrs,
		"NewSpecNode.kind enum must be exactly {feature, strategy} — the workflow controller routes dispatch by kind")
}
