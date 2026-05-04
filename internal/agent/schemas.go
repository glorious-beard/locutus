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

	// RevisionPlan is the spec_revision_triager agent's output. EVERY
	// critic finding routes to one of three buckets — there is no
	// "non-actionable, omit" bucket; the triager's authority is
	// routing only (DJ-095). Drives per-node revise fanouts (DJ-092)
	// and per-finding addition fanouts (DJ-095) downstream.
	// The schema example deliberately leaves StrategyRevisions empty
	// to show the model that an empty array is valid output. When the
	// example showed all three arrays populated (DJ-095 original
	// shape), the model pattern-matched the example shape and emitted
	// `strategy_revisions: [{}]` even when there were no strategy
	// revisions to make — a degenerate shape that wasted one
	// elaborator call per run. Showing an empty array here teaches
	// the model that "no entries" is a valid emission, not a hole to
	// be filled with a placeholder. See DJ-097 follow-up.
	//
	// FeatureRevisions and Additions stay populated so the model
	// still sees the typical-case shapes for the buckets that
	// usually carry entries.
	RegisterSchema("RevisionPlan", RevisionPlan{
		FeatureRevisions: []NodeRevision{{
			NodeID:   "feat-example",
			Concerns: []string{"verbatim text of the critic finding targeting this feature"},
		}},
		StrategyRevisions: []NodeRevision{},
		Additions: []AddedNode{{
			Kind:          "strategy",
			SourceConcern: "verbatim text of a critic finding proposing a missing feature or strategy; kind selects which elaborator agent the addition fanout dispatches to",
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
