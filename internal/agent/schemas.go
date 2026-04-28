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

	RegisterSchema("IntakeResult", IntakeResult{
		ID:              "feat-realtime-dashboard",
		Title:           "Real-time dashboard",
		Accepted:        true,
		Reason:          "aligns with project goals",
		SuggestedLabels: []string{"enhancement"},
	})

	// Spec-generation council outputs (agents in
	// internal/scaffold/agents/spec_*.md and *_critic.md).
	RegisterSchema("ScoutBrief", ScoutBrief{
		DomainRead:          "two-or-three-sentence read of the domain",
		TechnologyOptions:   []string{"frontend: A vs B vs C"},
		ImplicitAssumptions: []string{"scale: how many users? Default: 100k registered, 1k concurrent."},
		WatchOuts:           []string{"vendor lock-in to platform X"},
	})

	RegisterSchema("SpecProposal", SpecProposal{
		Features: []FeatureProposal{{
			ID:          "feat-example",
			Title:       "Example feature",
			Description: "What the feature does in one paragraph.",
			Decisions:   []string{"dec-example"},
		}},
		Decisions: []DecisionProposal{{
			ID:         "dec-example",
			Title:      "Example decision",
			Rationale:  "why this choice",
			Confidence: 0.8,
			Alternatives: []spec.Alternative{{
				Name:            "alternative",
				Rationale:       "why it was considered",
				RejectedBecause: "why it was rejected",
			}},
			Citations: []spec.Citation{{
				Kind:      "goals",
				Reference: "GOALS.md",
				Span:      "lines 6-8",
				Excerpt:   "verbatim quoted text from the source",
			}},
			ArchitectRationale: "one-sentence summary distinct from the longer rationale",
		}},
		Strategies: []StrategyProposal{{
			ID:    "strat-example",
			Title: "Example strategy",
			Kind:  "foundational",
			Body:  "prose body of the strategy",
		}},
		Approaches: []ApproachProposal{{
			ID:       "app-example",
			Title:    "Example approach",
			ParentID: "feat-example",
			Body:     "implementation sketch",
		}},
	})

	RegisterSchema("CriticIssues", CriticIssues{
		Issues: []string{"feature feat-x references dec-y but dec-y is not generated"},
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
