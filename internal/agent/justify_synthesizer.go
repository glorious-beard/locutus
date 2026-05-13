package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// SynthesisVerdict is the output of the justify_synthesizer agent.
// Aggregates per-decision verdicts (already produced by the
// per-decision RunJustifyAgainst calls) with a strategy-level read
// of the parent's body prose. The orchestrator combines this with
// the per-decision results into the final user-facing output.
type SynthesisVerdict struct {
	// Defense is the strategy-level defense paragraph: how the
	// parent as a whole holds up against the challenge given the
	// per-decision verdicts and the parent's own prose.
	Defense string `json:"defense" jsonschema:"description=Two to three paragraphs of strategy-level prose. Names which decisions held and which broke, what that means for the parent as a whole, and cites the relevant goal clauses. Even when verdict is held_up the defense explains why the parent stands."`

	// ParentProseAddress is the synthesizer's response to the
	// parent-prose shard from the splitter — the part of the user's
	// challenge that engaged the parent's body prose without
	// mapping to a specific decision. Empty when the splitter
	// produced no prose shard.
	ParentProseAddress string `json:"parent_prose_address,omitempty" jsonschema:"description=The synthesizer's response to the parent-prose shard from the splitter — the portion of the user's challenge that engaged the parent's body prose without mapping to a specific decision. Empty when the splitter produced no prose shard; do not invent content to fill it."`

	// Verdict aggregates the per-decision verdicts and the
	// prose-level concerns. One of held_up / partially_held_up /
	// broke_down — the enum constraint lands in the generated
	// JSON schema so providers with strict structured-output modes
	// (Gemini responseSchema, OpenAI json_schema strict) reject
	// non-enum values at the SDK level rather than letting them
	// reach the validator. Anthropic's tool-use mode is more
	// lenient about string contents, but the schema documentation
	// still signals intent to the model.
	Verdict string `json:"verdict" jsonschema:"enum=held_up,enum=partially_held_up,enum=broke_down"`

	// BreakingPoints carries the strategy-level synthesis of breaks.
	// Each breaking point names its source decision (when it traces
	// back to a per-decision verdict) so the suggested-next-step
	// renderer can route each break to the right `refine` target.
	BreakingPoints []StrategyBreakingPoint `json:"breaking_points,omitempty" jsonschema:"description=Every break that landed somewhere — from a per-decision verdict or from the parent-prose engagement. A single break that surfaced via both paths counts as one entry, sourced to the decision (more actionable for refine routing)."`

	// Rationale is a one-to-two-sentence summary of the aggregation
	// reasoning. Surfaced in traces and rendered output.
	Rationale string `json:"rationale,omitempty" jsonschema:"description=One to two sentences naming the aggregation choice and why. Not a paragraph. Not a re-derivation of the verdict — the verdict field carries the conclusion; this field carries the brief why."`
}

// StrategyBreakingPoint is a single break in the strategy-level
// verdict, tagged with the decision it traces back to (or empty
// when prose-only). The renderer reads SourceDecision to suggest
// `refine <dec-id> --supersede` per break, or `refine <parent-id>
// --brief` for prose-only breaks.
type StrategyBreakingPoint struct {
	Description    string `json:"description" jsonschema:"description=The breaking point in strategy-level voice — often a paraphrase of a per-decision break, surfaced at the parent level. A complete sentence naming what gave way."`
	SourceDecision string `json:"source_decision,omitempty" jsonschema:"description=The dec-id this break traces back to, when it came from a per-decision verdict. Empty for prose-only breaks. When set, must be one of the ids in the per-decision input list, verbatim."`
}

func init() {
	RegisterSchema("SynthesisVerdict", SynthesisVerdict{
		Defense:            "the strategy-level defense paragraph",
		ParentProseAddress: "the synthesizer's response to the parent-prose shard, when the splitter emitted one",
		Verdict:            "held_up",
		BreakingPoints: []StrategyBreakingPoint{
			{Description: "the breaking point as the synthesizer sees it", SourceDecision: "dec-source-id-or-empty-for-prose"},
		},
		Rationale: "one-to-two-sentence summary of the aggregation reasoning",
	})
}

// SynthesisInput bundles everything the synthesizer needs to roll up
// per-decision verdicts into a strategy-level read.
type SynthesisInput struct {
	ParentID    string
	ParentKind  spec.NodeKind
	ParentTitle string
	ParentProse string
	GoalsBody   string
	Challenge   string

	// PerDecisionResults are the verdicts from each fan-out
	// per-decision RunJustifyAgainst call. Orchestrator-produced;
	// the synthesizer reads but does not re-derive them.
	PerDecisionResults []PerDecisionResult

	// ParentProseShard is the splitter's parent-prose-only slice
	// of the user's challenge. Empty when the splitter found
	// nothing to engage at the prose layer.
	ParentProseShard string
}

// PerDecisionResult is one fan-out result the synthesizer aggregates.
// Mirrors what RunJustifyAgainst produces for a single-decision run.
type PerDecisionResult struct {
	DecisionID    string
	DecisionTitle string
	Challenge     string // the splitter's shard for this decision
	Verdict       string // held_up | partially_held_up | broke_down
	Defense       string
	BreakingPoints []string
}

// synthesizerMaxAttempts caps retry budget for the synthesizer.
// Three attempts cover the standard [anthropic, googleai, openai]
// rotation exactly once. Same reasoning as challengerMaxAttempts.
//
// Empirically motivated by a 2026-05-10 incident where Anthropic
// returned syntactically valid JSON but with the entire chain-of-
// thought stream stuffed into the `verdict` string field — a
// schema-skeleton-adjacent failure mode that single-attempt
// dispatch can't recover from.
const synthesizerMaxAttempts = 3

// InvokeSynthesizer runs the justify_synthesizer agent. Returns the
// aggregate strategy-level verdict. The dispatcher applies provider
// rotation + validator-driven retry (synthesizerMaxAttempts) when the
// output is degenerate (invalid verdict, empty defense, runaway field
// content).
func InvokeSynthesizer(ctx context.Context, dispatcher AgentDispatcher, def AgentDef, in SynthesisInput) (*SynthesisVerdict, error) {
	if len(in.PerDecisionResults) == 0 && strings.TrimSpace(in.ParentProseShard) == "" {
		return nil, fmt.Errorf("invoke synthesizer: nothing to synthesize (no per-decision results and no prose shard)")
	}

	user := buildSynthesizerPrompt(in)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}

	out, dispatchErr := dispatcher.Dispatch(ctx, def, input, DispatchOptions{
		Role:         "synthesis",
		OutputSchema: "SynthesisVerdict",
		MaxAttempts:  synthesizerMaxAttempts,
		Validator: func(out *AgentOutput) (string, bool) {
			var verdict SynthesisVerdict
			if jerr := json.Unmarshal([]byte(out.Content), &verdict); jerr != nil {
				return fmt.Sprintf("parse output: %s", jerr), true
			}
			if reason, degenerate := degenerateSynthesisVerdict(&verdict); degenerate {
				return reason, true
			}
			return "", false
		},
	})

	var verdict *SynthesisVerdict
	if out != nil {
		var parsed SynthesisVerdict
		if jerr := json.Unmarshal([]byte(out.Content), &parsed); jerr == nil {
			verdict = &parsed
		}
	}
	if dispatchErr != nil {
		return verdict, fmt.Errorf("invoke synthesizer: %w", dispatchErr)
	}
	if verdict == nil {
		return nil, fmt.Errorf("invoke synthesizer: parse output: dispatcher returned no usable content")
	}
	return verdict, nil
}

// degenerateSynthesisVerdict catches the chain-of-thought-into-
// output failure mode that produced multi-paragraph strings in the
// verdict field on 2026-05-10. Triggers any one of:
//
//   - Verdict isn't one of the three enum values (after trim).
//   - Defense is empty or longer than 8000 runes (synthesizer
//     spec is 2-3 paragraphs; runaway output is much longer).
//   - Rationale is empty or longer than 1500 runes.
//
// The length floors / ceilings are intentionally generous — the
// goal is to catch obviously broken output, not enforce prose
// quality. A real synthesizer response sits comfortably inside
// these bounds.
func degenerateSynthesisVerdict(v *SynthesisVerdict) (string, bool) {
	if v == nil {
		return "nil verdict", true
	}
	verdict := strings.TrimSpace(v.Verdict)
	if !validVerdict(verdict) {
		preview := verdict
		if len([]rune(preview)) > 80 {
			preview = string([]rune(preview)[:80]) + "…"
		}
		return fmt.Sprintf("verdict not in enum (got %q)", preview), true
	}
	defense := strings.TrimSpace(v.Defense)
	if defense == "" {
		return "defense is empty", true
	}
	if len([]rune(defense)) > 8000 {
		return fmt.Sprintf("defense is %d runes (ceiling 8000)", len([]rune(defense))), true
	}
	if len([]rune(strings.TrimSpace(v.Rationale))) > 1500 {
		return fmt.Sprintf("rationale is %d runes (ceiling 1500)", len([]rune(v.Rationale))), true
	}
	return "", false
}

func buildSynthesizerPrompt(in SynthesisInput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Parent node\n\n- ID: `%s`\n- Kind: %s\n- Title: %s\n",
		in.ParentID, in.ParentKind, in.ParentTitle)
	if strings.TrimSpace(in.ParentProse) != "" {
		b.WriteString("- Body prose:\n\n  ")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(in.ParentProse), "\n", "\n  "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if strings.TrimSpace(in.GoalsBody) != "" {
		b.WriteString("## Goals\n\n")
		b.WriteString(in.GoalsBody)
		b.WriteString("\n\n")
	}

	b.WriteString("## User's challenge\n\n")
	b.WriteString(in.Challenge)
	b.WriteString("\n\n")

	if len(in.PerDecisionResults) > 0 {
		b.WriteString("## Per-decision verdicts\n\n")
		for i, r := range in.PerDecisionResults {
			fmt.Fprintf(&b, "### %d. `%s` — %s\n\n", i+1, r.DecisionID, r.DecisionTitle)
			fmt.Fprintf(&b, "**Verdict:** %s\n\n", r.Verdict)
			if r.Challenge != "" {
				fmt.Fprintf(&b, "**Challenge slice:** %s\n\n", r.Challenge)
			}
			if r.Defense != "" {
				fmt.Fprintf(&b, "**Advocate defense:**\n\n%s\n\n", r.Defense)
			}
			if len(r.BreakingPoints) > 0 {
				b.WriteString("**Breaking points:**\n")
				for _, bp := range r.BreakingPoints {
					fmt.Fprintf(&b, "- %s\n", bp)
				}
				b.WriteString("\n")
			}
		}
	}

	if strings.TrimSpace(in.ParentProseShard) != "" {
		b.WriteString("## Parent-prose challenge shard\n\n")
		b.WriteString(in.ParentProseShard)
		b.WriteString("\n\nThe portion of the user's challenge above engages the parent's body prose without mapping to a specific decision. Address it in `parent_prose_address`.\n\n")
	}

	return b.String()
}
