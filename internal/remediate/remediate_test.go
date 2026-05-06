package remediate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/remediate"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptPlan returns a MockLLM that emits a single Plan JSON response.
func scriptPlan(t *testing.T, plan remediate.Plan) *agent.MockExecutor {
	t.Helper()
	payload, err := json.Marshal(plan)
	require.NoError(t, err)
	return agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: string(payload)},
	})
}

func TestRemediateNoGapsIsNoOp(t *testing.T) {
	llm := agent.NewMockExecutor() // no scripted responses; should never be called
	result, err := remediate.Remediate(context.Background(), llm, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, llm.CallCount(), "no gaps must skip the LLM call")
	assert.Equal(t, 0, result.DecisionsCreated)
	assert.Equal(t, 0, result.StrategiesCreated)
	assert.Equal(t, 0, result.FeaturesCreated)
	assert.Equal(t, 0, result.FeaturesUpdated)
}

func TestRemediateNilLLMReturnsError(t *testing.T) {
	gaps := []agent.Gap{{Category: "missing_test_framework", Severity: "high", Description: "no go test wired"}}
	_, err := remediate.Remediate(context.Background(), nil, gaps, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm")
}

func TestRemediateCreatesAssumedDecisions(t *testing.T) {
	gaps := []agent.Gap{{
		Category:    "missing_test_framework",
		Severity:    "high",
		Description: "no test runner configured",
	}}
	plan := remediate.Plan{
		Decisions: []spec.Decision{
			{ID: "dec-test-framework", Title: "Use go test", Status: spec.DecisionStatusAssumed, Rationale: "fills missing_test_framework"},
		},
	}
	llm := scriptPlan(t, plan)

	result, err := remediate.Remediate(context.Background(), llm, gaps, nil)
	require.NoError(t, err)
	require.NotNil(t, result.Plan)
	require.Len(t, result.Plan.Decisions, 1)
	assert.Equal(t, spec.DecisionStatusAssumed, result.Plan.Decisions[0].Status)
	assert.Equal(t, 1, result.DecisionsCreated)
}

func TestRemediateCrossCuttingGapsConsolidate(t *testing.T) {
	gaps := []agent.Gap{
		{Category: "missing_quality_strategy", Description: "no linter"},
		{Category: "missing_quality_strategy", Description: "no CI"},
	}
	plan := remediate.Plan{
		Features: []spec.Feature{
			{ID: "f-project-remediation", Title: "Project remediation", Status: spec.FeatureStatusInferred,
				Decisions: []string{"dec-add-linter", "dec-add-ci"}},
		},
		Decisions: []spec.Decision{
			{ID: "dec-add-linter", Title: "Add golangci-lint", Status: spec.DecisionStatusAssumed},
			{ID: "dec-add-ci", Title: "Add CI workflow", Status: spec.DecisionStatusAssumed},
		},
		Strategies: []spec.Strategy{
			{ID: "strat-lint", Title: "Linting strategy", Decisions: []string{"dec-add-linter"}, Status: "assumed"},
		},
	}
	llm := scriptPlan(t, plan)

	result, err := remediate.Remediate(context.Background(), llm, gaps, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.FeaturesCreated)
	assert.Equal(t, 2, result.DecisionsCreated)
	assert.Equal(t, 1, result.StrategiesCreated)
}

func TestRemediateFeatureSpecificGapsAttach(t *testing.T) {
	existing := &agent.ExistingSpec{
		Features: []spec.Feature{
			{ID: "f-auth", Title: "Authentication", Decisions: []string{"dec-jwt"}},
		},
	}
	gaps := []agent.Gap{{
		Category:    "missing_tests",
		Description: "auth feature has no tests",
		AffectedIDs: []string{"f-auth"},
	}}
	plan := remediate.Plan{
		Decisions: []spec.Decision{
			{ID: "dec-auth-tests", Title: "Add auth tests", Status: spec.DecisionStatusAssumed},
		},
		FeatureUpdates: []remediate.FeatureUpdate{
			{FeatureID: "f-auth", AddedDecisions: []string{"dec-auth-tests"}},
		},
	}
	llm := scriptPlan(t, plan)

	result, err := remediate.Remediate(context.Background(), llm, gaps, existing)
	require.NoError(t, err)
	assert.Equal(t, 1, result.DecisionsCreated)
	assert.Equal(t, 1, result.FeaturesUpdated)
	require.Len(t, result.Plan.FeatureUpdates, 1)
	assert.Equal(t, "f-auth", result.Plan.FeatureUpdates[0].FeatureID)
}

func TestRemediatePromptContainsGapsAndExistingSpec(t *testing.T) {
	existing := &agent.ExistingSpec{
		Features: []spec.Feature{{ID: "f-payment-sentinel", Title: "Payment processing"}},
	}
	gaps := []agent.Gap{{
		Category:    "missing_quality_strategy",
		Severity:    "high",
		Description: "no-linter-sentinel",
	}}
	llm := scriptPlan(t, remediate.Plan{})

	_, err := remediate.Remediate(context.Background(), llm, gaps, existing)
	require.NoError(t, err)

	calls := llm.Calls()
	require.Len(t, calls, 1)
	var prompt strings.Builder
	for _, m := range calls[0].Input.Messages {
		prompt.WriteString(m.Content)
		prompt.WriteString("\n")
	}
	p := prompt.String()

	assert.Contains(t, p, "missing_quality_strategy", "gap category")
	assert.Contains(t, p, "no-linter-sentinel", "gap description")
	assert.Contains(t, p, "f-payment-sentinel", "existing Feature ID surfaced for context")
}

func TestApplyToAssimilationMergesPlan(t *testing.T) {
	existing := &agent.ExistingSpec{
		Features: []spec.Feature{{ID: "f-auth", Title: "Auth", Decisions: []string{"dec-jwt"}}},
	}
	result := &agent.AssimilationResult{
		Decisions: []spec.Decision{{ID: "dec-jwt", Title: "Use JWT", Status: spec.DecisionStatusActive}},
	}
	plan := &remediate.Plan{
		Decisions: []spec.Decision{
			{ID: "dec-auth-tests", Title: "Add auth tests", Status: spec.DecisionStatusAssumed},
		},
		FeatureUpdates: []remediate.FeatureUpdate{
			{FeatureID: "f-auth", AddedDecisions: []string{"dec-auth-tests"}},
		},
	}

	remediate.ApplyToAssimilation(plan, result, existing)

	require.Len(t, result.Decisions, 2)
	assert.Equal(t, "dec-auth-tests", result.Decisions[1].ID)

	// f-auth pulled from existing into result.Features with the new
	// Decision appended so persistAssimilationResult writes it back.
	require.Len(t, result.Features, 1)
	assert.Equal(t, "f-auth", result.Features[0].ID)
	assert.ElementsMatch(t, []string{"dec-jwt", "dec-auth-tests"}, result.Features[0].Decisions)
}

func TestApplyToAssimilationFeatureUpdateOnNewFeature(t *testing.T) {
	// FeatureUpdate refers to a Feature created in the same plan — the
	// Feature should already be in plan.Features, FeatureUpdate then
	// merges Decisions into it.
	plan := &remediate.Plan{
		Features: []spec.Feature{
			{ID: "f-project-remediation", Title: "Project remediation", Decisions: []string{"dec-add-ci"}},
		},
		Decisions: []spec.Decision{{ID: "dec-add-lint", Status: spec.DecisionStatusAssumed}},
		FeatureUpdates: []remediate.FeatureUpdate{
			{FeatureID: "f-project-remediation", AddedDecisions: []string{"dec-add-lint"}},
		},
	}
	result := &agent.AssimilationResult{}

	remediate.ApplyToAssimilation(plan, result, nil)

	require.Len(t, result.Features, 1)
	assert.ElementsMatch(t, []string{"dec-add-ci", "dec-add-lint"}, result.Features[0].Decisions)
}
