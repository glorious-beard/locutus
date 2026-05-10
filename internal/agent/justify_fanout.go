package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// FanOutResult is the aggregate output of a fan-out justify against
// a strategy / feature / bug / approach. Combines the splitter's
// classification, the per-decision verdicts, and the synthesizer's
// strategy-level read.
type FanOutResult struct {
	Split              *ChallengeSplit
	PerDecisionResults []FanOutDecisionResult
	Synthesis          *SynthesisVerdict
}

// FanOutDecisionResult is one per-decision run's full output:
// challenger brief, research findings, adversarial defense, and the
// shard of the user's challenge that drove this decision-level run.
type FanOutDecisionResult struct {
	DecisionID    string
	DecisionTitle string
	Challenge     string
	Challenger    *ChallengeBrief
	Research      *ResearchBrief
	Adversarial   *AdversarialDefense
}

// FanOutInputs carries the per-target inputs the fan-out
// orchestrator needs in addition to JustifyInputs's per-agent
// AgentDefs. The cmd layer assembles this from the loaded spec
// graph.
type FanOutInputs struct {
	JustifyInputs

	// ParentNodeMD is the rendered explain output for the parent
	// node (strategy / feature / bug / approach). Used by the
	// synthesizer for body-prose context. The single-decision
	// flows under fan-out get their own per-decision NodeMarkdown
	// from PerDecisionMarkdown.
	ParentNodeMD string

	// ParentBody is the parent's body prose extracted from the
	// loaded graph. Splitter and synthesizer both consume it.
	ParentBody string

	// PerDecisionMarkdown maps decision id → rendered explain
	// markdown for that decision. The orchestrator uses these as
	// NodeMarkdown for each per-decision RunJustifyAgainst call.
	PerDecisionMarkdown map[string]string

	// DecisionRefs is the ordered list of decisions referenced by
	// the parent. Order matters for splitter shard alignment.
	DecisionRefs []SplitterDecisionRef

	// Splitter / Synthesizer carry the loaded scaffold AgentDefs.
	// JustifyInputs already carries Advocate / Challenger /
	// Researcher.
	Splitter    AgentDef
	Synthesizer AgentDef
}

// RunJustifyFanOut orchestrates a fan-out justify against a parent
// node. Splits the challenge across decisions, runs the existing
// adversarial flow per decision in parallel, then synthesizes a
// strategy-level verdict from the per-decision verdicts plus any
// parent-prose engagement.
//
// On per-decision failure: the orchestrator collects the first
// error and aborts. Half-failed fan-out runs aren't synthesized —
// surface the error so the user can re-run or scope to specific
// decisions via --decisions. (Future: partial-result mode.)
func RunJustifyFanOut(ctx context.Context, dispatcher AgentDispatcher, in FanOutInputs) (*FanOutResult, error) {
	if strings.TrimSpace(in.Challenge) == "" {
		return nil, fmt.Errorf("justify fan-out: empty challenge")
	}
	if len(in.DecisionRefs) == 0 {
		return nil, fmt.Errorf("justify fan-out: parent %q references no decisions", in.NodeID)
	}

	// Step 1: splitter classifies the challenge across decisions.
	splitterIn := SplitterInput{
		ParentID:    in.NodeID,
		ParentKind:  nodeKindFromID(in.NodeID),
		ParentTitle: in.ParentTitleOrID(),
		ParentProse: in.ParentBody,
		Decisions:   in.DecisionRefs,
		Challenge:   in.Challenge,
	}
	split, err := InvokeSplitter(ctx, dispatcher, in.Splitter, splitterIn)
	if err != nil {
		return nil, fmt.Errorf("justify fan-out: split: %w", err)
	}

	// Step 2: dispatch per-decision RunJustifyAgainst for shards
	// with non-empty content. Empty shards mean the splitter
	// determined the challenge doesn't engage that decision —
	// skip rather than burn tokens on a forced run.
	relevant := make([]int, 0, len(split.DecisionShards))
	for i, shard := range split.DecisionShards {
		if strings.TrimSpace(shard.Shard) != "" {
			relevant = append(relevant, i)
		}
	}
	if len(relevant) == 0 && strings.TrimSpace(split.ParentProseShard) == "" {
		return &FanOutResult{Split: split}, fmt.Errorf("justify fan-out: splitter produced no per-decision shards and no parent-prose shard for %q — challenge may not engage this node", in.NodeID)
	}

	// Sequential fan-out. Parallel dispatch was tempting (matches
	// council fan-out patterns) but each per-decision flow fires
	// 3 LLM calls — running multiple in parallel means N concurrent
	// grounded researcher calls competing for provider rate budgets,
	// and the mock-driven tests can't easily bind tagged responses
	// to specific goroutines. Sequential is simpler, deterministic,
	// and the latency for typical 2-3-decision fan-outs isn't bad
	// (~30-60s real wall-clock). Revisit if real-world fan-outs
	// hit a strategy with 5+ decisions where serialisation hurts.
	results := make([]FanOutDecisionResult, 0, len(relevant))
	for _, refIdx := range relevant {
		ref := in.DecisionRefs[refIdx]
		shard := split.DecisionShards[refIdx].Shard
		perIn := JustifyInputs{
			NodeID:       ref.ID,
			NodeMarkdown: in.PerDecisionMarkdown[ref.ID],
			GoalsBody:    in.GoalsBody,
			Challenge:    shard,
			Advocate:     in.Advocate,
			Challenger:   in.Challenger,
			Researcher:   in.Researcher,
		}
		challenger, research, defense, perErr := RunJustifyAgainst(ctx, dispatcher, perIn)
		if perErr != nil {
			return &FanOutResult{Split: split, PerDecisionResults: results},
				fmt.Errorf("justify fan-out: decision %s: %w", ref.ID, perErr)
		}
		results = append(results, FanOutDecisionResult{
			DecisionID:    ref.ID,
			DecisionTitle: ref.Title,
			Challenge:     shard,
			Challenger:    challenger,
			Research:      research,
			Adversarial:   defense,
		})
	}

	// Step 3: synthesizer aggregates per-decision verdicts +
	// parent-prose shard into a strategy-level verdict.
	synthIn := SynthesisInput{
		ParentID:           in.NodeID,
		ParentKind:         nodeKindFromID(in.NodeID),
		ParentTitle:        in.ParentTitleOrID(),
		ParentProse:        in.ParentBody,
		GoalsBody:          in.GoalsBody,
		Challenge:          in.Challenge,
		PerDecisionResults: toSynthInputs(results),
		ParentProseShard:   split.ParentProseShard,
	}
	synth, err := InvokeSynthesizer(ctx, dispatcher, in.Synthesizer, synthIn)
	if err != nil {
		return &FanOutResult{Split: split, PerDecisionResults: results}, fmt.Errorf("justify fan-out: synthesize: %w", err)
	}

	return &FanOutResult{
		Split:              split,
		PerDecisionResults: results,
		Synthesis:          synth,
	}, nil
}

// toSynthInputs flattens FanOutDecisionResult into the shape the
// synthesizer agent's input bundle expects.
func toSynthInputs(results []FanOutDecisionResult) []PerDecisionResult {
	out := make([]PerDecisionResult, len(results))
	for i, r := range results {
		var defense string
		var breakingPoints []string
		var verdict string
		if r.Adversarial != nil {
			defense = r.Adversarial.Defense
			breakingPoints = r.Adversarial.BreakingPoints
			verdict = r.Adversarial.Verdict
		}
		out[i] = PerDecisionResult{
			DecisionID:     r.DecisionID,
			DecisionTitle:  r.DecisionTitle,
			Challenge:      r.Challenge,
			Verdict:        verdict,
			Defense:        defense,
			BreakingPoints: breakingPoints,
		}
	}
	return out
}

// ParentTitleOrID returns the parent's title if non-empty, otherwise
// the id. Convenience for splitter / synthesizer prompts.
func (in FanOutInputs) ParentTitleOrID() string {
	// JustifyInputs doesn't carry a Title field today — we render
	// from NodeMarkdown's first heading line if needed; for now
	// just fall back to NodeID. Title lookups against the loaded
	// graph happen at the cmd layer where the *spec.Loaded is
	// available.
	if in.ParentNodeMD != "" {
		// Best-effort: extract the first H1 from the rendered
		// explain output. Format is typically "# `<id>` — <title>"
		// per render/explain.go.
		if title := firstH1Title(in.ParentNodeMD); title != "" {
			return title
		}
	}
	return in.NodeID
}

// firstH1Title extracts the title from "# `<id>` — <title>" headers.
// Returns empty when the input doesn't match the pattern.
func firstH1Title(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		if idx := strings.Index(line, " — "); idx >= 0 {
			return strings.TrimSpace(line[idx+len(" — "):])
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "# "))
	}
	return ""
}

// nodeKindFromID maps an id prefix to the corresponding NodeKind.
// Returns empty NodeKind for unknown prefixes; callers expecting a
// kind validate up-front.
func nodeKindFromID(id string) spec.NodeKind {
	switch {
	case strings.HasPrefix(id, "dec-"):
		return spec.KindDecision
	case strings.HasPrefix(id, "feat-"):
		return spec.KindFeature
	case strings.HasPrefix(id, "strat-"):
		return spec.KindStrategy
	case strings.HasPrefix(id, "bug-"):
		return spec.KindBug
	case strings.HasPrefix(id, "app-"):
		return spec.KindApproach
	}
	return ""
}
