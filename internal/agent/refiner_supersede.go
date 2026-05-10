package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// RewriteDecisionResult is the JSON the refiner-supersede-decision
// agent returns. RevisedDecision carries the full replacement struct
// (id, title, status, confidence, alternatives, rationale,
// influenced_by, provenance); the caller validates schema-level
// invariants and id-prefix correctness before applying.
//
// Rationale is a short architect-voice summary of the supersession
// reasoning that flows into the history event. One or two sentences
// — the structured fields carry the durable record.
type RewriteDecisionResult struct {
	RevisedDecision spec.Decision `json:"revised_decision"`
	Rationale       string        `json:"rationale"`
}

// RewriteFeatureResult is the JSON the refiner-supersede-feature
// agent returns.
type RewriteFeatureResult struct {
	RevisedFeature spec.Feature `json:"revised_feature"`
	Rationale      string       `json:"rationale"`
}

// RewriteStrategyResult is the JSON the refiner-supersede-strategy
// agent returns.
type RewriteStrategyResult struct {
	RevisedStrategy spec.Strategy `json:"revised_strategy"`
	Rationale       string        `json:"rationale"`
}

func init() {
	RegisterSchema("RewriteDecisionResult", RewriteDecisionResult{
		RevisedDecision: spec.Decision{
			ID:         "dec-replacement-slug",
			Title:      "Replacement decision title",
			Status:     spec.DecisionStatusProposed,
			Confidence: 0.8,
			Rationale:  "why this replaces the prior decision",
			Alternatives: []spec.Alternative{{
				Name:            "previously chosen option",
				Rationale:       "why it was originally chosen",
				RejectedBecause: "the breaking-point reasoning",
			}},
		},
		Rationale: "one-line architect summary of the supersession",
	})
	RegisterSchema("RewriteFeatureResult", RewriteFeatureResult{
		RevisedFeature: spec.Feature{
			ID:                 "feat-replacement-slug",
			Title:              "Replacement feature title",
			Status:             spec.FeatureStatusProposed,
			Description:        "what this feature delivers",
			AcceptanceCriteria: []string{"criterion A", "criterion B"},
		},
		Rationale: "one-line architect summary of the supersession",
	})
	RegisterSchema("RewriteStrategyResult", RewriteStrategyResult{
		RevisedStrategy: spec.Strategy{
			ID:     "strat-replacement-slug",
			Title:  "Replacement strategy title",
			Kind:   spec.StrategyKindFoundational,
			Status: "proposed",
		},
		Rationale: "one-line architect summary of the supersession",
	})
}

// SupersedeContext carries the inputs every refiner-supersede agent
// needs. The caller assembles this from the loaded spec graph and
// the user-supplied motivation.
type SupersedeContext struct {
	OldNode        any    // *spec.Decision, *spec.Feature, or *spec.Strategy
	Motivation     string // user's authoritative directive
	JustifySession string // optional pointer at the .locutus/sessions file (DJ-085)
}

// InvokeSupersedeDecision runs the refiner-supersede-decision agent
// against ctx, returning the parsed result. The caller is responsible
// for validating that result.RevisedDecision.ID is the expected new
// slug and applying via cascade.ApplySupersedeDecision.
func InvokeSupersedeDecision(ctx context.Context, llm AgentExecutor, def AgentDef, sctx SupersedeContext) (*RewriteDecisionResult, error) {
	old, ok := sctx.OldNode.(*spec.Decision)
	if !ok || old == nil {
		return nil, fmt.Errorf("invoke supersede decision: old node must be *spec.Decision, got %T", sctx.OldNode)
	}

	user := buildSupersedeDecisionPrompt(*old, sctx.Motivation, sctx.JustifySession)
	out, err := llm.Run(ctx, def, AgentInput{Messages: []Message{{Role: "user", Content: user}}})
	if err != nil {
		return nil, fmt.Errorf("invoke supersede decision: %w", err)
	}
	var result RewriteDecisionResult
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil {
		return nil, fmt.Errorf("invoke supersede decision: parse output: %w", err)
	}
	if strings.TrimSpace(result.RevisedDecision.ID) == "" {
		return nil, fmt.Errorf("invoke supersede decision: agent returned empty id")
	}
	return &result, nil
}

// InvokeSupersedeFeature runs the refiner-supersede-feature agent.
func InvokeSupersedeFeature(ctx context.Context, llm AgentExecutor, def AgentDef, sctx SupersedeContext) (*RewriteFeatureResult, error) {
	old, ok := sctx.OldNode.(*spec.Feature)
	if !ok || old == nil {
		return nil, fmt.Errorf("invoke supersede feature: old node must be *spec.Feature, got %T", sctx.OldNode)
	}

	user := buildSupersedeFeaturePrompt(*old, sctx.Motivation, sctx.JustifySession)
	out, err := llm.Run(ctx, def, AgentInput{Messages: []Message{{Role: "user", Content: user}}})
	if err != nil {
		return nil, fmt.Errorf("invoke supersede feature: %w", err)
	}
	var result RewriteFeatureResult
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil {
		return nil, fmt.Errorf("invoke supersede feature: parse output: %w", err)
	}
	if strings.TrimSpace(result.RevisedFeature.ID) == "" {
		return nil, fmt.Errorf("invoke supersede feature: agent returned empty id")
	}
	return &result, nil
}

// InvokeSupersedeStrategy runs the refiner-supersede-strategy agent.
func InvokeSupersedeStrategy(ctx context.Context, llm AgentExecutor, def AgentDef, sctx SupersedeContext) (*RewriteStrategyResult, error) {
	old, ok := sctx.OldNode.(*spec.Strategy)
	if !ok || old == nil {
		return nil, fmt.Errorf("invoke supersede strategy: old node must be *spec.Strategy, got %T", sctx.OldNode)
	}

	user := buildSupersedeStrategyPrompt(*old, sctx.Motivation, sctx.JustifySession)
	out, err := llm.Run(ctx, def, AgentInput{Messages: []Message{{Role: "user", Content: user}}})
	if err != nil {
		return nil, fmt.Errorf("invoke supersede strategy: %w", err)
	}
	var result RewriteStrategyResult
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil {
		return nil, fmt.Errorf("invoke supersede strategy: parse output: %w", err)
	}
	if strings.TrimSpace(result.RevisedStrategy.ID) == "" {
		return nil, fmt.Errorf("invoke supersede strategy: agent returned empty id")
	}
	return &result, nil
}

// buildSupersedeDecisionPrompt assembles the user-message body. The
// system prompt comes from the agent .md (output_schema injects the
// JSON shape). The user message carries the structured payload —
// existing decision JSON, motivation, optional justify session
// pointer.
func buildSupersedeDecisionPrompt(old spec.Decision, motivation, justifySession string) string {
	var b strings.Builder
	b.WriteString("# Supersession context\n\n")
	b.WriteString("**Existing decision (to be replaced):**\n\n```json\n")
	if data, err := json.MarshalIndent(old, "", "  "); err == nil {
		b.Write(data)
	} else {
		fmt.Fprintf(&b, "%q (marshal failed: %v)", old.ID, err)
	}
	b.WriteString("\n```\n\n")

	b.WriteString("**Motivation (user's authoritative directive):**\n\n")
	b.WriteString(motivation)
	b.WriteString("\n\n")

	if justifySession != "" {
		fmt.Fprintf(&b, "**Justify session pointer:** `%s` (informational; the breaking-point analysis lives in the motivation above)\n\n", justifySession)
	}

	b.WriteString("# Your task\n\n")
	b.WriteString("Emit a replacement Decision that addresses the motivation. Preserve every alternative from the old decision and add the option that prompted supersession (with its rejection rationale derived from the breaking-point analysis). Set status and confidence per your own judgment of the new state. The id field must be a slug derived from the new title.\n")
	return b.String()
}

func buildSupersedeFeaturePrompt(old spec.Feature, motivation, justifySession string) string {
	var b strings.Builder
	b.WriteString("# Supersession context\n\n")
	b.WriteString("**Existing feature (to be replaced):**\n\n```json\n")
	if data, err := json.MarshalIndent(old, "", "  "); err == nil {
		b.Write(data)
	} else {
		fmt.Fprintf(&b, "%q (marshal failed: %v)", old.ID, err)
	}
	b.WriteString("\n```\n\n")

	b.WriteString("**Motivation (user's authoritative directive):**\n\n")
	b.WriteString(motivation)
	b.WriteString("\n\n")

	if justifySession != "" {
		fmt.Fprintf(&b, "**Justify session pointer:** `%s`\n\n", justifySession)
	}

	b.WriteString("# Your task\n\n")
	b.WriteString("Emit a replacement Feature that addresses the motivation. Acceptance criteria from the old feature must carry forward unless the motivation explicitly retires them. Decisions and Approaches lists carry forward unchanged — supersession changes the feature's identity, not its decisions. The id field must be a slug derived from the new title.\n")
	return b.String()
}

func buildSupersedeStrategyPrompt(old spec.Strategy, motivation, justifySession string) string {
	var b strings.Builder
	b.WriteString("# Supersession context\n\n")
	b.WriteString("**Existing strategy (to be replaced):**\n\n```json\n")
	if data, err := json.MarshalIndent(old, "", "  "); err == nil {
		b.Write(data)
	} else {
		fmt.Fprintf(&b, "%q (marshal failed: %v)", old.ID, err)
	}
	b.WriteString("\n```\n\n")

	b.WriteString("**Motivation (user's authoritative directive):**\n\n")
	b.WriteString(motivation)
	b.WriteString("\n\n")

	if justifySession != "" {
		fmt.Fprintf(&b, "**Justify session pointer:** `%s`\n\n", justifySession)
	}

	b.WriteString("# Your task\n\n")
	b.WriteString("Emit a replacement Strategy that addresses the motivation. Prerequisites and skills carry forward unless the motivation drops them. Strategy kind carries forward unless the motivation explicitly changes it. Decisions and Approaches lists carry forward unchanged. The id field must be a slug derived from the new title.\n")
	return b.String()
}
