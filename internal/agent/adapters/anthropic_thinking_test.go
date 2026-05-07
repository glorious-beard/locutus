package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestThinkingAllowed_RespectsAnthropicConstraint locks in the gate
// that solved the user-visible failure on spec_scout's first
// dispatch under the DJ-107 reorder:
//
//	"Thinking may not be enabled when tool_choice forces tool use."
//
// Strict-mode JSON enforcement on Anthropic uses forced tool_choice
// (the synthetic schema tool). Anthropic's API rejects extended
// thinking on those calls. The gate must return false whenever
// forced tool-use is engaged so the adapter skips the Thinking
// config block; otherwise return true (thinking + free tool choice
// coexist fine, as do thinking + no tools at all).
func TestThinkingAllowed_RespectsAnthropicConstraint(t *testing.T) {
	cases := []struct {
		name                string
		useForcedSchemaTool bool
		want                bool
	}{
		{name: "no forced tool — thinking allowed", useForcedSchemaTool: false, want: true},
		{name: "forced schema tool — thinking suppressed", useForcedSchemaTool: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, thinkingAllowed(tc.useForcedSchemaTool))
		})
	}
}
