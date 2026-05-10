package agent

import (
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// FanOutResult is the aggregate output of a fan-out justify against
// a strategy / feature / bug / approach. Combines the splitter's
// classification, the per-decision verdicts, and the synthesizer's
// strategy-level read.
//
// Phase-5 workflowization: the legacy RunJustifyFanOut function
// (along with FanOutInputs) was removed; the cmd layer now drives
// JustifyAdversarialFanoutWorkflow and assembles this struct from
// the workflow's per-step state. The result shape is unchanged so
// downstream renderers and JSON consumers stay byte-identical.
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
