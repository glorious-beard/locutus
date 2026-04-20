package agent

import "github.com/chetan/locutus/internal/spec"

func init() {
	// Register spec types for output_schema injection.
	// When an agent's frontmatter has output_schema: "MasterPlan",
	// BuildGenerateRequest appends the JSON representation of this
	// example struct to the system prompt as a schema reference.
	RegisterSchema("MasterPlan", spec.MasterPlan{
		ID:      "plan-XXX",
		Version: 1,
		Workstreams: []spec.Workstream{{
			ID:             "ws-XXX",
			StrategyDomain: "domain",
			DetailLevel:    spec.DetailLevelHigh,
			Steps: []spec.PlanStep{{
				ID:          "step-1",
				Order:       1,
				ApproachID:  "strat-XXX",
				Description: "description of what to do",
				Assertions: []spec.Assertion{{
					Kind:    spec.AssertionKindTestPass,
					Target:  "./pkg/...",
					Message: "all tests pass",
				}},
			}},
		}},
		Summary: "human-readable plan summary",
	})

	RegisterSchema("TriageVerdict", TriageVerdict{
		Accepted:        true,
		Reason:          "aligns with project goals",
		SuggestedLabels: []string{"enhancement"},
	})

	RegisterSchema("Concern", Concern{
		AgentID:  "critic",
		Severity: "high",
		Text:     "description of the concern",
	})

	RegisterSchema("Finding", Finding{
		Query:  "the question investigated",
		Result: "evidence-based analysis",
	})
}
