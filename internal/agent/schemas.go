package agent

import (
	"encoding/json"

	"github.com/chetan/locutus/internal/spec"
	"github.com/invopop/jsonschema"
)

func init() {
	// Register spec types for output_schema injection.
	// When an agent's frontmatter has output_schema: "MasterPlan",
	// BuildGenerateRequest appends the JSON representation of this
	// example struct to the system prompt as a schema reference.
	// MasterPlan example payload: under DJ-121 the primary acceptance
	// contract lives at workstream-level Assertions, not on PlanSteps.
	// The planner is encouraged (via this schema example, the planner
	// prompt, and the field's DEPRECATED doc) to emit workstream-level
	// criteria. PlanStep.Assertions is retained as a transitional fallback
	// but not surfaced in the example payload — Phase 9 removes PlanStep
	// entirely once all consumers have migrated.
	RegisterSchema("MasterPlan", spec.MasterPlan{
		ID:      "plan-XXX",
		Version: 1,
		Workstreams: []spec.Workstream{{
			ID:             "ws-XXX",
			StrategyDomain: "domain",
			DetailLevel:    spec.DetailLevelHigh,
			Assertions: []spec.Assertion{{
				Kind:    spec.AssertionKindTestPass,
				Target:  "./pkg/...",
				Message: "all tests pass when this workstream completes",
			}, {
				Kind:    spec.AssertionKindCompiles,
				Message: "the whole package compiles after this workstream's changes land",
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
		Summary:    "Adopt X over Y for the OLTP store.",
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
			Summary:     "One-sentence what-the-feature-does, ending with a period.",
			Title:       "Example feature",
			Description: "What the feature does in one paragraph.",
			Decisions:   []InlineDecisionProposal{exampleInlineDecision},
		}},
		Strategies: []RawStrategyProposal{{
			ID:        "strat-example",
			Summary:   "One-sentence what-the-strategy-adopts, ending with a period.",
			Title:     "Example strategy",
			Kind:      "foundational",
			Body:      "prose body of the strategy",
			Decisions: []InlineDecisionProposal{exampleInlineDecision},
		}},
	})

	RegisterSchema("SpecProposal", SpecProposal{
		Features: []FeatureProposal{{
			ID:          "feat-example",
			Summary:     "One-sentence what-the-feature-does, ending with a period.",
			Title:       "Example feature",
			Description: "What the feature does in one paragraph.",
			Decisions:   []string{"dec-example"},
		}},
		Decisions: []DecisionProposal{{
			ID:         "dec-example",
			Summary:    "Adopt X over Y for the OLTP store.",
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
			ID:      "strat-example",
			Summary: "One-sentence what-the-strategy-adopts, ending with a period.",
			Title:   "Example strategy",
			Kind:    "foundational",
			Body:    "prose body of the strategy",
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
		Summary:     "One-sentence what-the-feature-does, ending with a period.",
		Title:       "Example feature",
		Description: "What the feature does in one paragraph.",
		Decisions:   []InlineDecisionProposal{exampleInlineDecision},
	})

	RegisterSchema("RawStrategyProposal", RawStrategyProposal{
		ID:        "strat-example",
		Summary:   "One-sentence what-the-strategy-adopts, ending with a period.",
		Title:     "Example strategy",
		Kind:      "foundational",
		Body:      "prose body of the strategy",
		Decisions: []InlineDecisionProposal{exampleInlineDecision},
	})

	// RawDecisionProposal is the per-axis output shape of DJ-124's
	// Phase 1 decision-elaborator. The example payload uses descriptive
	// domain vocabulary (a database-engine choice grounded in a
	// GOALS.md clause and a vendor doc) so the schema-skeleton
	// failure mode the prompt-doc renderer can prime does not trigger
	// on placeholder tokens.
	RegisterSchema("RawDecisionProposal", RawDecisionProposal{
		ID:                 "dec-postgres-oltp-store",
		Summary:            "Adopt Postgres over MySQL for the OLTP store.",
		Title:              "OLTP store engine",
		Rationale:          "Postgres offers richer transactional guarantees and the JSONB column type the analytics workload depends on, while MySQL's storage-engine pluralism is irrelevant to the project's single-node deployment posture.",
		ArchitectRationale: "Postgres aligns with the JSONB-leaning analytics queries and the single-engine simplification.",
		Confidence:         0.8,
		Alternatives: []spec.Alternative{{
			Name:            "MySQL",
			Rationale:       "Familiar to the team and a common default at this scale.",
			RejectedBecause: "JSONB-equivalent storage is bolted on rather than first-class, which fights the analytics roadmap in GOALS.md.",
			Citations: []spec.Citation{{
				Kind:      "doc",
				Reference: "https://dev.mysql.com/doc/refman/8.0/en/json.html",
				Excerpt:   "JSON values are stored as native JSON, but indexing requires generated columns.",
			}},
		}},
		Citations: []spec.Citation{{
			Kind:      "goals",
			Reference: "GOALS.md",
			Span:      "## Analytics workload",
			Excerpt:   "The store must support ad-hoc JSON queries against the events table.",
		}},
		Axes:       []string{"oltp-store"},
		SurfacedBy: []string{"feat-realtime-dashboard"},
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

	// Hand-authored override for ReconciliationVerdict. The Go struct
	// is flat with everything `,omitempty` (because each kind needs a
	// different subset of the fields), so reflection produces a
	// permissive schema. The model exploits the freedom: observed in
	// the wild on Gemini Pro emitting dedupe actions with no
	// `canonical` despite the prompt saying it's required, which
	// downstream blows up ApplyReconciliation.
	//
	// Discriminated union by `kind` makes the API itself reject
	// malformed actions:
	//   - dedupe          → required: kind, sources, canonical
	//   - resolve_conflict → required: kind, sources, canonical, loser, rejected_because
	//   - reuse_existing  → required: kind, sources, existing_id
	RegisterSchemaOverride("ReconciliationVerdict", buildReconciliationVerdictSchema())

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

	// SpecGateVerdict drives the DJ-122 spec-council convergence loop.
	// The spec_gate agent reads the assembled ProposedSpec + GOALS.md
	// and grades it against the four-lifecycle-phases YES question
	// (define / develop / deploy / support). Example payload uses
	// descriptive prose, never placeholder tokens — placeholders prime
	// the schema-skeleton failure mode the validators exist to catch.
	RegisterSchema("SpecGateVerdict", SpecGateVerdict{
		Converged: false,
		Reasoning: "Define and develop are committed; deploy carries a weak commitment and support has a genuine gap.",
		OpenDimensions: []OpenDimension{
			{
				Deliverable:             "iOS companion app",
				Phase:                   "deploy",
				Axis:                    "App Store / TestFlight rollout cadence",
				Reasoning:               "Distribution channel is named but the staged-rollout cadence between TestFlight and App Store is not committed; the team cannot decide release tagging without it.",
				CurrentCommitmentQuoted: "Releases ship to the App Store via Fastlane.",
			},
			{
				Deliverable:             "nRF52840 firmware",
				Phase:                   "deploy",
				Axis:                    "OTA update channel",
				Reasoning:               "The firmware deliverable has no OTA path committed; without one the team cannot ship a security fix after the first device ships.",
				CurrentCommitmentQuoted: "",
			},
			{
				Deliverable:             "Vapor backend",
				Phase:                   "support",
				Axis:                    "incident response runbook structure",
				Reasoning:               "Datadog and SLOs are committed but the proposal never says where runbooks live or how they're authored; on-call engineers will have alerts without a response playbook.",
				CurrentCommitmentQuoted: "Observability is provided via Datadog with OpenTelemetry, tracking p99 latency and error-rate SLOs at 99.9%.",
			},
		},
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

	RegisterSchema("JustificationBrief", JustificationBrief{
		Defense:                     "two to four paragraphs of prose argument naming the goal-clauses being satisfied, why the chosen path beats the listed alternatives, and what trade-offs are accepted.",
		GoalClausesCited:            []string{"verbatim excerpt from GOALS.md the defense relies on"},
		ConditionsUnderWhichInvalid: []string{"a constraint change that would prompt revisiting this node"},
	})

	RegisterSchema("ResearchBrief", ResearchBrief{
		Findings: []Finding{{
			Query:  "the factual question raised by a challenger concern",
			Result: "evidence-based analysis citing retrieved sources",
		}},
	})

	RegisterSchema("ChallengeBrief", ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the specific weakness in the chosen approach",
			Evidence:        "GOALS clause, search result, or known pattern that supports the concern",
			Counterproposal: "an alternative or test that would resolve the question",
		}},
	})

	RegisterSchema("AdversarialDefense", AdversarialDefense{
		JustificationBrief: JustificationBrief{
			Defense:                     "two to four paragraphs of prose addressing the specific challenge.",
			GoalClausesCited:            []string{"verbatim excerpt from GOALS.md"},
			ConditionsUnderWhichInvalid: []string{"a constraint change that would prompt revisiting this node"},
		},
		PointByPointAddressed: []AddressedConcern{{
			ConcernSummary: "one-line restatement of the challenger's concern",
			Response:       "the advocate's response paragraph",
			StillStands:    true,
		}},
		Verdict:        "held_up",
		BreakingPoints: []string{"specific gap in the original rationale that the challenge surfaced"},
	})
}

// buildReconciliationVerdictSchema authors the JSON Schema for the
// reconciler's output. Sub-shapes (source ref, inline decision) are
// reflected from their Go structs to stay in sync with the canonical
// types.
//
// DJ-111: the schema is a flat object with `kind` enum and all
// variant-specific fields optional. The Go-side ReconciliationAction
// is already flat (all variant fields tagged omitempty); the apply
// switch in reconcile.go does per-kind validation downstream. The
// earlier discriminated-union shape (oneOf of three variants, each
// with its own required-fields set) was schema-only — consumer code
// never relied on it. Anthropic's native output_config rejects oneOf
// (Gemini and OpenAI accept it), so this flatter shape works
// uniformly across providers.
//
// Per-kind required fields drift to prompt guidance + apply-time
// validation, the same posture the rest of the codebase uses for
// every other LLM output. The strict-schema layer enforces that
// `kind` and `sources` are present and that `kind` is one of the
// known enum values; everything else gets validated on apply.
func buildReconciliationVerdictSchema() map[string]any {
	sourceSchema := reflectStrictSchema(DecisionSourceRef{})
	inlineSchema := reflectStrictSchema(InlineDecisionProposal{})

	actionItem := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"kind", "sources"},
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string",
				"enum": []any{"dedupe", "resolve_conflict", "reuse_existing"},
			},
			"sources":          map[string]any{"type": "array", "items": sourceSchema},
			"canonical":        inlineSchema,
			"loser":            inlineSchema,
			"rejected_because": map[string]any{"type": "string"},
			"existing_id":      map[string]any{"type": "string"},
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"actions"},
		"properties": map[string]any{
			"actions": map[string]any{
				"type":  "array",
				"items": actionItem,
			},
		},
	}
}

// reflectStrictSchema returns a JSON Schema map for the given example
// value, with stripJSONSchemaArtifacts + enforceStrict applied so the
// shape matches what the rest of the pipeline produces. Used to build
// sub-schemas for hand-authored discriminated unions without hand-
// authoring every field.
func reflectStrictSchema(example any) map[string]any {
	r := jsonschema.Reflector{
		AllowAdditionalProperties:  false,
		DoNotReference:             true,
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: false,
	}
	reflected := r.Reflect(example)
	data, err := json.Marshal(reflected)
	if err != nil {
		// Static example values; failure here is a programming bug.
		panic("reflectStrictSchema marshal: " + err.Error())
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		panic("reflectStrictSchema unmarshal: " + err.Error())
	}
	stripJSONSchemaArtifacts(schema)
	enforceStrict(schema)
	return schema
}
