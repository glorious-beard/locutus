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

// buildReconciliationVerdictSchema authors the discriminated-union
// JSON Schema for the reconciler's output. Sub-shapes (source ref,
// inline decision) are reflected from their Go structs to stay in
// sync with the canonical types; the top-level oneOf is hand-rolled
// because invopop can't express "different required fields per
// kind" from struct tags alone.
//
// Each variant pins `kind` via an enum of one literal so the model
// commits to a discriminator at output time. Strict-mode adapters
// (Anthropic forced tool-use, Gemini responseJsonSchema, OpenAI
// json_schema strict) all honor oneOf with enum discriminants.
func buildReconciliationVerdictSchema() map[string]any {
	sourceSchema := reflectStrictSchema(DecisionSourceRef{})
	inlineSchema := reflectStrictSchema(InlineDecisionProposal{})

	dedupeAction := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"kind", "sources", "canonical"},
		"properties": map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []any{"dedupe"}},
			"sources":   map[string]any{"type": "array", "items": sourceSchema},
			"canonical": inlineSchema,
		},
	}
	resolveAction := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"kind", "sources", "canonical", "loser", "rejected_because"},
		"properties": map[string]any{
			"kind":             map[string]any{"type": "string", "enum": []any{"resolve_conflict"}},
			"sources":          map[string]any{"type": "array", "items": sourceSchema},
			"canonical":        inlineSchema,
			"loser":            inlineSchema,
			"rejected_because": map[string]any{"type": "string"},
		},
	}
	reuseAction := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"kind", "sources", "existing_id"},
		"properties": map[string]any{
			"kind":        map[string]any{"type": "string", "enum": []any{"reuse_existing"}},
			"sources":     map[string]any{"type": "array", "items": sourceSchema},
			"existing_id": map[string]any{"type": "string"},
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"actions"},
		"properties": map[string]any{
			"actions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"oneOf": []any{dedupeAction, resolveAction, reuseAction},
				},
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
