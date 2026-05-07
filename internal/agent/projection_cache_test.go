package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectElaborateOne_SplitsCacheableFromVariable confirms
// (DJ-106) that the per-node elaborator projection emits the static
// prefix (GOALS, scout brief, outline) as a Cacheable=true Message
// followed by a Cacheable=false Message carrying the per-call
// fanout target. The Anthropic adapter places a cache_control marker
// on the cacheable block so the council fanout's 15-25 calls all
// share one cached prefix instead of paying for it on every dispatch.
func TestProjectElaborateOne_SplitsCacheableFromVariable(t *testing.T) {
	snap := StateSnapshot{
		Prompt:     "GOALS body verbatim",
		ScoutBrief: `{"domain_read":"a domain","technology_options":["A","B"],"implicit_assumptions":["scale: small"],"watch_outs":["w1"]}`,
		Outline:    `{"features":[{"id":"feat-x","title":"X","summary":"sx"}],"strategies":[{"id":"strat-y","title":"Y","kind":"foundational","summary":"sy"}]}`,
		FanoutItem: `{"id":"feat-x","title":"X","summary":"sx"}`,
	}

	out := projectElaborateOne(snap, "feature")

	require.GreaterOrEqual(t, len(out), 2, "elaborator projection must emit at least 2 messages so the static prefix can be marked Cacheable separately from the per-call variation")

	// First message: cacheable static prefix.
	assert.True(t, out[0].Cacheable, "first message must be marked Cacheable=true (static prefix)")
	assert.Equal(t, "user", out[0].Role)
	assert.Contains(t, out[0].Content, "GOALS body verbatim",
		"cacheable prefix must include GOALS")
	assert.Contains(t, out[0].Content, "## Scout brief",
		"cacheable prefix must include scout brief")
	assert.Contains(t, out[0].Content, "## Outline",
		"cacheable prefix must include outline")
	assert.NotContains(t, out[0].Content, "feature to elaborate",
		"cacheable prefix must NOT include the per-call variation")

	// Last message: variable per-call payload.
	last := out[len(out)-1]
	assert.False(t, last.Cacheable, "per-call variation must be Cacheable=false")
	assert.Equal(t, "user", last.Role)
	assert.Contains(t, last.Content, "feature to elaborate",
		"variable suffix must carry the per-call target header")
	assert.Contains(t, last.Content, "feat-x",
		"variable suffix must carry the per-call target id")
}
