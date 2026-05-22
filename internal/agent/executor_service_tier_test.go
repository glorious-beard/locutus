package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTierToServiceTier_Mapping locks in the per-tier policy DJ-132
// follow-up established:
//
//   - fast     → ServiceTierFlex     (cheaper; best-effort latency)
//   - balanced → ServiceTierStandard (default cost/latency)
//   - strong   → ServiceTierPriority (latency-stable for council gating agents)
//   - unknown / empty → ServiceTierUnset (adapter falls through to provider default)
//
// The fast→Flex case is the load-bearing one: every per-axis
// candidate-survey fanout runs at fast tier, so this single mapping
// shifts the whole DJ-132 fanout onto Flex pricing automatically.
func TestTierToServiceTier_Mapping(t *testing.T) {
	cases := []struct {
		tier string
		want adapters.ServiceTier
	}{
		{string(TierFast), adapters.ServiceTierFlex},
		{string(TierBalanced), adapters.ServiceTierStandard},
		{string(TierStrong), adapters.ServiceTierPriority},
		{"", adapters.ServiceTierUnset},
		{"unknown", adapters.ServiceTierUnset},
	}
	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			assert.Equal(t, tc.want, tierToServiceTier(tc.tier))
		})
	}
}

// TestBuildAdapterRequest_ThreadsServiceTier verifies the executor
// populates Request.ServiceTier from the picked tier. The mapping is
// covered by TestTierToServiceTier_Mapping above; this test locks in
// the integration point (build site reads pick.Tier and applies the
// mapper). Without this, a future refactor of buildAdapterRequest
// could silently drop the field and the DJ-132 cost story would
// regress without breaking a single existing test.
func TestBuildAdapterRequest_ThreadsServiceTier(t *testing.T) {
	def := AgentDef{ID: "agent", SystemPrompt: "system"}
	input := AgentInput{Messages: []Message{{Role: "user", Content: "do the work"}}}

	for _, tc := range []struct {
		tier string
		want adapters.ServiceTier
	}{
		{string(TierFast), adapters.ServiceTierFlex},
		{string(TierBalanced), adapters.ServiceTierStandard},
		{string(TierStrong), adapters.ServiceTierPriority},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			pick := &ResolvedModel{
				Provider: ProviderGoogleAI,
				Tier:     tc.tier,
				Model:    "gemini-3.5-flash",
				Thinking: adapters.ThinkingOff,
			}
			req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
			require.NoError(t, err)
			assert.Equal(t, tc.want, req.ServiceTier,
				"buildAdapterRequest must thread the per-tier ServiceTier so adapters see the right cost/latency posture")
		})
	}
}
