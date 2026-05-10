package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSuggestedNextStep_BrokeDownAgainstDecisionRoutesToSupersede(t *testing.T) {
	got := suggestedNextStep("dec-foo", "broke_down", []string{"missing alternative"})
	assert.Contains(t, got, "--supersede",
		"BROKE DOWN against decision must route to --supersede")
	assert.Contains(t, got, "dec-foo")
	assert.Contains(t, got, "missing alternative",
		"the breaking-point text must be threaded into the --supersede argument")
	assert.NotContains(t, got, "--brief",
		"--brief and --supersede are mutually exclusive in the suggestion")
}

func TestSuggestedNextStep_BrokeDownAgainstFeatureRoutesToSupersede(t *testing.T) {
	got := suggestedNextStep("feat-foo", "broke_down", []string{"scope wrong"})
	assert.Contains(t, got, "--supersede")
	assert.Contains(t, got, "feat-foo")
}

func TestSuggestedNextStep_BrokeDownAgainstStrategyRoutesToSupersede(t *testing.T) {
	got := suggestedNextStep("strat-foo", "broke_down", []string{"architectural shift"})
	assert.Contains(t, got, "--supersede")
	assert.Contains(t, got, "strat-foo")
}

func TestSuggestedNextStep_BrokeDownAgainstBugRoutesToBrief(t *testing.T) {
	got := suggestedNextStep("bug-foo", "broke_down", []string{"x"})
	assert.Contains(t, got, "--brief",
		"BROKE DOWN against bug must stay on --brief; supersede doesn't accept bugs")
	assert.NotContains(t, got, "--supersede")
}

func TestSuggestedNextStep_BrokeDownAgainstApproachRoutesToBrief(t *testing.T) {
	got := suggestedNextStep("app-foo", "broke_down", []string{"x"})
	assert.Contains(t, got, "--brief",
		"BROKE DOWN against approach must stay on --brief; supersede only handles decision/feature/strategy")
	assert.NotContains(t, got, "--supersede")
}

func TestSuggestedNextStep_HeldUpAgainstDecisionStaysOnBrief(t *testing.T) {
	got := suggestedNextStep("dec-foo", "held_up", []string{"minor concern"})
	assert.Contains(t, got, "--brief",
		"held_up verdict against decision uses the existing prose-cascade path")
	assert.NotContains(t, got, "--supersede")
}

func TestSuggestedNextStep_PartiallyHeldUpAgainstDecisionStaysOnBrief(t *testing.T) {
	got := suggestedNextStep("dec-foo", "partially_held_up", []string{"weak point"})
	assert.Contains(t, got, "--brief")
	assert.NotContains(t, got, "--supersede")
}

func TestSuggestedNextStep_QuotesArgumentSafely(t *testing.T) {
	// Breaking points may contain quotes, newlines, or backslashes.
	// The shell-safe quoting comes from %q — verify it's applied.
	got := suggestedNextStep("dec-foo", "broke_down",
		[]string{`alternative "WorkOS" was missing`})
	// The quoted form must escape the inner quotes so a copy-paste
	// into a shell doesn't break.
	assert.Contains(t, got, `\"WorkOS\"`,
		"inner double-quotes must be escaped for shell paste-safety")
}
