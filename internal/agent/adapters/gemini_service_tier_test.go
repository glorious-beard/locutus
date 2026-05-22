package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

// TestGeminiServiceTier_PerTierMapping locks in DJ-132 follow-up's
// per-tier ServiceTier policy. The fast→Flex mapping is load-bearing
// for the candidate-survey fanout's cost story: every per-axis survey
// dispatches at fast tier; routing them through Flex shifts the
// fanout from default-pricing to Flex pricing automatically without
// any changes to agent prompts or workflow definitions.
//
// The balanced→Standard mapping is the pre-DJ-132-followup
// behaviour — exercise it explicitly so a future regression that
// drops the case can't silently downgrade balanced calls to Flex.
//
// The strong→Priority mapping is latency policy for the council's
// gating agents (scout, decision-elaborator, critic-elaborator).
// Their critical-path position makes Flex's best-effort latency the
// wrong tradeoff at this tier.
func TestGeminiServiceTier_PerTierMapping(t *testing.T) {
	cases := []struct {
		neutral ServiceTier
		want    genai.ServiceTier
		ok      bool
		reason  string
	}{
		{ServiceTierFlex, genai.ServiceTierFlex, true, "fast tier maps to Flex (cheaper; best-effort latency)"},
		{ServiceTierStandard, genai.ServiceTierStandard, true, "balanced tier maps to Standard (default cost/latency)"},
		{ServiceTierPriority, genai.ServiceTierPriority, true, "strong tier maps to Priority (latency-stable; council gating agents)"},
		{ServiceTierUnset, genai.ServiceTier(""), false, "unset tier returns (zero, false) so the adapter skips the ServiceTier assignment and the provider default applies"},
	}
	for _, tc := range cases {
		t.Run(string(tc.neutral), func(t *testing.T) {
			got, ok := geminiServiceTier(tc.neutral)
			assert.Equal(t, tc.ok, ok, tc.reason)
			assert.Equal(t, tc.want, got, tc.reason)
		})
	}
}
