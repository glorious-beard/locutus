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

	// RawSpecProposal is the architect's pre-reconcile output: features and
	// strategies with inline decisions, no IDs, no cross-array references.
	// The reconciler agent's verdict + ApplyReconciliation produce the
	// canonical SpecProposal that downstream agents and persistence consume.
	exampleInlineDecision := InlineDecisionProposal{
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
			Excerpt:   "verbatim quoted text from the source",
		}},
		ArchitectRationale: "one-sentence summary distinct from the longer rationale",
	}
	RegisterSchema("RawSpecProposal", RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:          "feat-example",
			Title:       "Example feature",
			Description: "What the feature does in one paragraph.",
			Decisions:   []InlineDecisionProposal{exampleInlineDecision},
		}},
		Strategies: []RawStrategyProposal{{
			ID:        "strat-example",
			Title:     "Example strategy",
			Kind:      "foundational",
			Body:      "prose body of the strategy",
			Decisions: []InlineDecisionProposal{exampleInlineDecision},
		}},
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
	})

	RegisterSchema("Outline", Outline{
		Features: []OutlineFeature{{
			ID:      "feat-example",
			Title:   "Example feature",
			Summary: "one-line summary of what this feature does",
		}},
		Strategies: []OutlineStrategy{{
			ID:      "strat-example",
			Title:   "Example strategy",
			Kind:    "foundational",
			Summary: "one-line summary of the cross-cutting choice",
		}},
	})

	RegisterSchema("RawFeatureProposal", RawFeatureProposal{
		ID:          "feat-example",
		Title:       "Example feature",
		Description: "What the feature does in one paragraph.",
		Decisions:   []InlineDecisionProposal{exampleInlineDecision},
	})

	RegisterSchema("RawStrategyProposal", RawStrategyProposal{
		ID:        "strat-example",
		Title:     "Example strategy",
		Kind:      "foundational",
		Body:      "prose body of the strategy",
		Decisions: []InlineDecisionProposal{exampleInlineDecision},
	})

	RegisterSchema("ReconciliationVerdict", ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "dedupe",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-example", Index: 0},
				{ParentKind: "strategy", ParentID: "strat-example", Index: 0},
			},
			Canonical: &exampleInlineDecision,
		}},
	})

	RegisterSchema("CriticIssues", CriticIssues{
		Issues: []string{"feature feat-x references dec-y but dec-y is not generated"},
	})

	// LLMFindingClusters is the spec_finding_clusterer agent's output
	// (DJ-098). The clusterer's only job is to group unmatched critic
	// findings by topic and assign each cluster a kind (feature or
	// strategy) so the workflow knows which elaborator to dispatch.
	// This replaces the three-bucket RevisionPlan: one schema, one
	// array, one decision dimension per cluster — eliminates the
	// schema-pattern-matching pathology that broke the triager three
	// times in a row.
	RegisterSchema("LLMFindingClusters", LLMFindingClusters{
		Clusters: []LLMFindingCluster{{
			Topic:    "infrastructure-as-code and CI/CD",
			Findings: []string{"verbatim text of a critic finding belonging to this cluster"},
			Kind:     "strategy",
		}},
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
