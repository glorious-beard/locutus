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
	Defense string `json:"defense"`

	// ParentProseAddress is the synthesizer's response to the
	// parent-prose shard from the splitter — the part of the user's
	// challenge that engaged the parent's body prose without
	// mapping to a specific decision. Empty when the splitter
	// produced no prose shard.
	ParentProseAddress string `json:"parent_prose_address,omitempty"`

	// Verdict aggregates the per-decision verdicts and the
	// prose-level concerns. One of held_up / partially_held_up /
	// broke_down.
	Verdict string `json:"verdict"`

	// BreakingPoints carries the strategy-level synthesis of breaks.
	// Each breaking point names its source decision (when it traces
	// back to a per-decision verdict) so the suggested-next-step
	// renderer can route each break to the right `refine` target.
	BreakingPoints []StrategyBreakingPoint `json:"breaking_points,omitempty"`

	// Rationale is a one-to-two-sentence summary of the aggregation
	// reasoning. Surfaced in traces and rendered output.
	Rationale string `json:"rationale,omitempty"`
}

// StrategyBreakingPoint is a single break in the strategy-level
// verdict, tagged with the decision it traces back to (or empty
// when prose-only). The renderer reads SourceDecision to suggest
// `refine <dec-id> --supersede` per break, or `refine <parent-id>
// --brief` for prose-only breaks.
type StrategyBreakingPoint struct {
	Description    string `json:"description"`
	SourceDecision string `json:"source_decision,omitempty"`
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

// InvokeSynthesizer runs the justify_synthesizer agent. Returns the
// aggregate strategy-level verdict.
func InvokeSynthesizer(ctx context.Context, llm AgentExecutor, def AgentDef, in SynthesisInput) (*SynthesisVerdict, error) {
	if len(in.PerDecisionResults) == 0 && strings.TrimSpace(in.ParentProseShard) == "" {
		return nil, fmt.Errorf("invoke synthesizer: nothing to synthesize (no per-decision results and no prose shard)")
	}

	def.OutputSchema = "SynthesisVerdict"
	user := buildSynthesizerPrompt(in)
	output, err := llm.Run(WithRole(ctx, "synthesis"), def, AgentInput{Messages: []Message{{Role: "user", Content: user}}})
	if err != nil {
		return nil, fmt.Errorf("invoke synthesizer: %w", err)
	}
	var verdict SynthesisVerdict
	if err := json.Unmarshal([]byte(output.Content), &verdict); err != nil {
		return nil, fmt.Errorf("invoke synthesizer: parse output: %w", err)
	}
	if !validVerdict(verdict.Verdict) {
		return &verdict, fmt.Errorf("invoke synthesizer: returned invalid verdict %q (want held_up|partially_held_up|broke_down)", verdict.Verdict)
	}
	return &verdict, nil
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
