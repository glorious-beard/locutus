package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRawProposalSchemasRequireDecisions locks in DJ-105's fix as
// preserved under DJ-124: both RawFeatureProposal and
// RawStrategyProposal MUST require a non-empty Decisions array in
// their strict-mode JSON schema. Under DJ-124 the field type flipped
// from `[]InlineDecisionProposal` to `[]string` (decision id
// references rather than inline objects); the minItems=1 constraint
// applies to arrays regardless of element type, so the strict-mode
// rejection of decision-less responses at the API layer still holds.
//
// Strict-mode enforcement at the API layer is tighter than any
// post-receive validation we could add: the model cannot return a
// non-conformant response in the first place; the executor's retry
// loop kicks in instead.
func TestRawProposalSchemasRequireDecisions(t *testing.T) {
	for _, name := range []string{"RawFeatureProposal", "RawStrategyProposal"} {
		t.Run(name, func(t *testing.T) {
			schema, err := SchemaFor(name)
			require.NoError(t, err)

			required, _ := schema["required"].([]any)
			requiredStrs := make([]string, 0, len(required))
			for _, r := range required {
				if s, ok := r.(string); ok {
					requiredStrs = append(requiredStrs, s)
				}
			}
			assert.Contains(t, requiredStrs, "decisions",
				"%s strict-mode schema must mark `decisions` as required so providers reject decision-less responses at the API layer", name)

			props, _ := schema["properties"].(map[string]any)
			require.NotNil(t, props, "%s schema missing properties map", name)
			decisions, _ := props["decisions"].(map[string]any)
			require.NotNil(t, decisions, "%s schema missing decisions property", name)

			minItems, ok := decisions["minItems"]
			require.True(t, ok, "%s.decisions must declare minItems to forbid empty arrays at the API layer", name)
			// JSON numbers come back as float64 from the round-trip
			// in SchemaFor; accept either typing for portability.
			switch v := minItems.(type) {
			case int:
				assert.GreaterOrEqual(t, v, 1, "%s.decisions.minItems must be >= 1", name)
			case float64:
				assert.GreaterOrEqual(t, v, float64(1), "%s.decisions.minItems must be >= 1", name)
			default:
				t.Fatalf("%s.decisions.minItems has unexpected type %T", name, minItems)
			}
		})
	}
}

// TestRawDecisionProposalSchemaRequiresGroundedFields locks in the
// DJ-124 Phase 1 contract on RawDecisionProposal: the per-axis
// decision-elaborator output MUST surface (a) the axes the decision
// answers, (b) the surfacing-node back-references, (c) the alternatives
// weighed, and (d) the citations backing the chosen path — all with
// minItems=1 and a non-empty description in the strict-mode JSON
// schema.
//
// Without minItems on the array fields, a flaky model could emit a
// structurally-valid response with empty arrays — the workflow
// controller would then fail to thread the decision into the affected
// feature/strategy nodes (axes never close; back-references stay
// hollow), or persist an ungrounded decision (the failure mode DJ-124
// exists to eliminate). The strict-mode schema rejects the response at
// the API layer instead, kicking the retry loop.
//
// Without descriptions, the model has no inline guidance on what these
// fields mean; the schema-skeleton failure mode (axes=["axis"] or
// alternatives=[{name:"alt"}]) becomes proportionally more likely.
func TestRawDecisionProposalSchemaRequiresGroundedFields(t *testing.T) {
	schema, err := SchemaFor("RawDecisionProposal")
	require.NoError(t, err, "RawDecisionProposal must be registered for the DJ-124 Phase 1 decision-elaborator")

	props, _ := schema["properties"].(map[string]any)
	require.NotNil(t, props, "RawDecisionProposal schema missing properties map")

	for _, field := range []string{"axes", "surfaced_by", "alternatives", "citations"} {
		t.Run(field, func(t *testing.T) {
			node, _ := props[field].(map[string]any)
			require.NotNil(t, node, "RawDecisionProposal schema missing %s property", field)

			minItems, ok := node["minItems"]
			require.True(t, ok, "RawDecisionProposal.%s must declare minItems to forbid empty arrays at the API layer", field)
			switch v := minItems.(type) {
			case int:
				assert.GreaterOrEqual(t, v, 1, "RawDecisionProposal.%s.minItems must be >= 1", field)
			case float64:
				assert.GreaterOrEqual(t, v, float64(1), "RawDecisionProposal.%s.minItems must be >= 1", field)
			default:
				t.Fatalf("RawDecisionProposal.%s.minItems has unexpected type %T", field, minItems)
			}

			desc, _ := node["description"].(string)
			assert.NotEmpty(t, desc, "RawDecisionProposal.%s must declare a description so the model has inline guidance on what to populate", field)
		})
	}
}
