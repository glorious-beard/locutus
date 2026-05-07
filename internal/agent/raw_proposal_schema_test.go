package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRawProposalSchemasRequireDecisions locks in Bug C's fix
// (DJ-105): both RawFeatureProposal and RawStrategyProposal MUST
// require a non-empty Decisions array in their strict-mode JSON
// schema. Prior to DJ-105, `Decisions []InlineDecisionProposal
// json:"decisions,omitempty"` made the field optional in strict
// mode, so providers like gemini-3.1-pro-preview could emit
// structurally-valid responses without any decisions and the
// elaborator dispatch path would accept them — leading to dangling-
// reference validation failures downstream.
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
