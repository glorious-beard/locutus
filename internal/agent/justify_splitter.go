package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// ChallengeSplit is the output of the justify_splitter agent. It
// classifies a user's challenge against a parent node (strategy /
// feature / bug / approach) into per-decision shards plus an
// optional parent-prose shard for claims that engage the parent's
// body prose without mapping to any specific decision.
//
// The splitter is a fast-tier classification call that runs once per
// fan-out justify; the per-decision shards then drive independent
// challenger → researcher → advocate cycles, and the synthesis
// agent rolls those up alongside the parent-prose shard into a
// single strategy-level verdict.
type ChallengeSplit struct {
	// DecisionShards has exactly one entry per decision in the
	// splitter's input, in input order. Shard is empty when the
	// challenge does not address that decision; the orchestrator
	// skips empty shards rather than firing a wasted decision-
	// level run.
	DecisionShards []DecisionShard `json:"decision_shards" jsonschema:"description=Exactly one entry per input decision, in input order. IDs must match the input decision IDs verbatim; emit empty Shard for decisions the challenge does not address rather than omitting them."`

	// ParentProseShard is the portion of the challenge that engages
	// the parent's own body prose — claims like "the rationale
	// argues for hiring velocity but..." that don't target any
	// specific decision. Empty when the challenge has no parent-
	// prose-level component. Consumed by the synthesis agent.
	ParentProseShard string `json:"parent_prose_shard,omitempty" jsonschema:"description=The portion of the challenge that engages the parent's body prose without mapping to any single decision. Empty when no such portion exists; do not invent content to fill it."`

	// Rationale is a one-line summary of how the splitter divided
	// the challenge. Surfaced in traces and the rendered output.
	Rationale string `json:"rationale,omitempty" jsonschema:"description=One-line summary of how the challenge was divided across decisions and parent prose. Surfaced in traces and the rendered output."`
}

// DecisionShard pairs a decision id with the slice of the user's
// challenge that addresses it. Empty Shard means the challenge does
// not address this decision; the orchestrator filters those out.
type DecisionShard struct {
	DecisionID string `json:"decision_id" jsonschema:"description=The decision id verbatim from the input list. Must be one of the ids passed in, character-for-character."`
	Shard      string `json:"shard" jsonschema:"description=The slice of the user's challenge that addresses this decision. Empty when the challenge does not address this decision; the orchestrator skips empty shards rather than firing a wasted decision-level run."`
}

func init() {
	RegisterSchema("ChallengeSplit", ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-example-one", Shard: "the portion of the challenge that addresses this decision"},
			{DecisionID: "dec-example-two", Shard: ""},
		},
		ParentProseShard: "the portion of the challenge that engages the parent's body prose without mapping to a specific decision",
		Rationale:        "one-line summary of how the challenge was split across decisions",
	})
}

// SplitterInput bundles the inputs the justify_splitter needs to
// classify a user challenge across a parent's referenced decisions.
type SplitterInput struct {
	ParentID    string
	ParentKind  spec.NodeKind
	ParentTitle string
	ParentProse string

	Decisions []SplitterDecisionRef

	Challenge string
}

// SplitterDecisionRef carries the minimum context the splitter needs
// to classify per decision: the id (output identifier), the title
// (most informative classifier signal), and a short rationale
// snippet (helps the splitter reason about content overlap with the
// challenge text). Truncated rationales are fine — this is a
// classification step, not a deep read.
type SplitterDecisionRef struct {
	ID        string
	Title     string
	Rationale string
}

// splitterMaxAttempts caps retry budget for the splitter. Three
// attempts cover the standard [anthropic, googleai, openai]
// rotation exactly once. Same reasoning as the challenger and
// synthesizer.
const splitterMaxAttempts = 3

// InvokeSplitter runs the justify_splitter agent. Returns a
// ChallengeSplit with one shard per input decision in input order.
// The caller drives fan-out by walking DecisionShards and dispatching
// the per-decision flow only for shards with non-empty Shard text.
//
// The dispatcher applies provider rotation + validator-driven retry
// (splitterMaxAttempts) when the output is unparseable or fails the
// shard-count / id-correspondence check.
//
// def is the loaded scaffold AgentDef; the dispatcher overrides
// OutputSchema to "ChallengeSplit" so the cmd layer doesn't have to
// remember the binding.
func InvokeSplitter(ctx context.Context, dispatcher AgentDispatcher, def AgentDef, in SplitterInput) (*ChallengeSplit, error) {
	if strings.TrimSpace(in.Challenge) == "" {
		return nil, fmt.Errorf("invoke splitter: challenge is empty")
	}
	if len(in.Decisions) == 0 {
		return nil, fmt.Errorf("invoke splitter: no decisions to classify against")
	}

	user := buildSplitterPrompt(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	out, dispatchErr := dispatcher.Dispatch(ctx, def, input, DispatchOptions{
		Role:         "split",
		OutputSchema: "ChallengeSplit",
		MaxAttempts:  splitterMaxAttempts,
		Validator: func(out *AgentOutput) (string, bool) {
			var split ChallengeSplit
			if jerr := json.Unmarshal([]byte(out.Content), &split); jerr != nil {
				return fmt.Sprintf("parse output: %s", jerr), true
			}
			if verr := validateSplit(&split, in.Decisions); verr != nil {
				return verr.Error(), true
			}
			return "", false
		},
	})

	var split *ChallengeSplit
	if out != nil {
		var parsed ChallengeSplit
		if jerr := json.Unmarshal([]byte(out.Content), &parsed); jerr == nil {
			split = &parsed
		}
	}
	if dispatchErr != nil {
		return split, fmt.Errorf("invoke splitter: %w", dispatchErr)
	}
	if split == nil {
		return nil, fmt.Errorf("invoke splitter: parse output: dispatcher returned no usable content")
	}
	return split, nil
}

// validateSplit enforces the contract: one shard per input decision
// in input order, with matching DecisionID values. A splitter that
// drops decisions or emits unknown ids is producing schema-skeleton
// output and we'd rather error loudly than fan out against a
// half-classified set.
func validateSplit(split *ChallengeSplit, decisions []SplitterDecisionRef) error {
	if len(split.DecisionShards) != len(decisions) {
		return fmt.Errorf("invoke splitter: shard count %d does not match decision count %d", len(split.DecisionShards), len(decisions))
	}
	for i, shard := range split.DecisionShards {
		if shard.DecisionID != decisions[i].ID {
			return fmt.Errorf("invoke splitter: shard[%d] id %q does not match expected decision id %q", i, shard.DecisionID, decisions[i].ID)
		}
	}
	return nil
}

// buildSplitterPrompt assembles the user message. The system prompt
// (loaded from internal/scaffold/agents/justify_splitter.md) sets
// the classification rules; this builder gives the splitter the
// concrete inputs it classifies against.
func buildSplitterPrompt(in SplitterInput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Parent node\n\n- ID: `%s`\n- Kind: %s\n- Title: %s\n",
		in.ParentID, in.ParentKind, in.ParentTitle)
	if strings.TrimSpace(in.ParentProse) != "" {
		b.WriteString("- Body prose:\n\n  ")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(in.ParentProse), "\n", "\n  "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("## Decisions referenced by this parent\n\n")
	for i, d := range in.Decisions {
		fmt.Fprintf(&b, "%d. `%s` — %s\n", i+1, d.ID, d.Title)
		if strings.TrimSpace(d.Rationale) != "" {
			fmt.Fprintf(&b, "   Rationale snippet: %s\n", strings.TrimSpace(d.Rationale))
		}
	}
	b.WriteString("\n")

	b.WriteString("## User's challenge\n\n")
	b.WriteString(in.Challenge)
	b.WriteString("\n")

	return b.String()
}
