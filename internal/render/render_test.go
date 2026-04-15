package render_test

import (
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/check"
	"github.com/chetan/locutus/internal/render"
	"github.com/stretchr/testify/assert"
)

func TestStatusSummaryEmpty(t *testing.T) {
	data := render.StatusData{
		GoalsPresent:  false,
		FeatureCount:  0,
		BugCount:      0,
		DecisionCount: 0,
		StrategyCount: 0,
		EntityCount:   0,
		OrphanCount:   0,
	}

	out := render.StatusSummary(data)

	assert.Contains(t, out, "GOALS.md")
	// Should indicate GOALS.md is missing / not found.
	lower := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lower, "not found") || strings.Contains(lower, "missing"),
		"expected missing-goals indicator, got: %s", out,
	)
	// All counts should be zero.
	assert.Contains(t, out, "0")
}

func TestStatusSummaryPopulated(t *testing.T) {
	data := render.StatusData{
		GoalsPresent:  true,
		FeatureCount:  3,
		BugCount:      1,
		DecisionCount: 5,
		StrategyCount: 4,
		EntityCount:   2,
		OrphanCount:   0,
	}

	out := render.StatusSummary(data)

	assert.Contains(t, out, "GOALS.md")
	// Should NOT say "not found" / "missing".
	lower := strings.ToLower(out)
	assert.False(t,
		strings.Contains(lower, "not found") || strings.Contains(lower, "missing"),
		"should not indicate goals missing when present, got: %s", out,
	)

	// Verify each count appears in the output.
	assert.Contains(t, out, "3", "feature count")
	assert.Contains(t, out, "1", "bug count")
	assert.Contains(t, out, "5", "decision count")
	assert.Contains(t, out, "4", "strategy count")
	assert.Contains(t, out, "2", "entity count")
}

func TestStatusSummaryWithOrphans(t *testing.T) {
	data := render.StatusData{
		GoalsPresent:  true,
		FeatureCount:  3,
		BugCount:      1,
		DecisionCount: 5,
		StrategyCount: 4,
		EntityCount:   2,
		OrphanCount:   2,
	}

	out := render.StatusSummary(data)

	// Orphan warning should be visible.
	lower := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lower, "orphan") || strings.Contains(lower, "warning"),
		"expected orphan warning, got: %s", out,
	)
	assert.Contains(t, out, "2", "orphan count")
}

func TestCheckResultsAllPass(t *testing.T) {
	results := []check.Result{
		{
			StrategyID:    "strat-auth",
			StrategyTitle: "JWT Authentication",
			Passed:        []string{"go module exists", "jwt library installed"},
			Failed:        nil,
		},
	}

	out := render.CheckResults(results)

	assert.Contains(t, out, "JWT Authentication")
	// Should show pass indicators for each prerequisite.
	assert.Contains(t, out, "go module exists")
	assert.Contains(t, out, "jwt library installed")
	// Should NOT contain failure-related language.
	lower := strings.ToLower(out)
	assert.False(t,
		strings.Contains(lower, "fail"),
		"should not contain failure language when all pass, got: %s", out,
	)
}

func TestCheckResultsWithFailures(t *testing.T) {
	results := []check.Result{
		{
			StrategyID:    "strat-db",
			StrategyTitle: "PostgreSQL Storage",
			Passed:        []string{"go module exists"},
			Failed: []check.CheckFailure{
				{
					Prerequisite: "postgres running",
					Err:       "connection refused on localhost:5432",
				},
				{
					Prerequisite: "migrations applied",
					Err:       "migration tool not found",
				},
			},
		},
	}

	out := render.CheckResults(results)

	assert.Contains(t, out, "PostgreSQL Storage")
	assert.Contains(t, out, "go module exists")
	assert.Contains(t, out, "postgres running")
	assert.Contains(t, out, "connection refused on localhost:5432")
	assert.Contains(t, out, "migrations applied")
	assert.Contains(t, out, "migration tool not found")
}

func TestVersion(t *testing.T) {
	out := render.Version("1.2.3")

	assert.Contains(t, out, "1.2.3")
	// Should contain "locutus" or project name.
	lower := strings.ToLower(out)
	assert.True(t,
		strings.Contains(lower, "locutus"),
		"expected project name in version output, got: %s", out,
	)
}
