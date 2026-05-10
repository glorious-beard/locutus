package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureFanOut builds a project with one strategy that references
// two decisions, plus the GOALS.md file the justify flow reads.
// Sized to exercise the fan-out path: the splitter sees two
// decisions, the orchestrator dispatches two per-decision flows,
// the synthesizer aggregates.
func fixtureFanOut(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	require.NoError(t, fs.WriteFile(".borg/GOALS.md", []byte("# Goals\n\nbuild a thing"), 0o644))

	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-framework", spec.Decision{
		ID: "dec-framework", Title: "Standardise on Next.js",
		Status: spec.DecisionStatusProposed, Confidence: 0.9,
		Rationale: "Next.js gives RSC and Vercel-shaped deployment.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-rsc", spec.Decision{
		ID: "dec-rsc", Title: "Use React Server Components",
		Status: spec.DecisionStatusProposed, Confidence: 0.85,
		Rationale: "RSC reduces client bundle and keeps data on the server.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-frontend", spec.Strategy{
		ID: "strat-frontend", Title: "Frontend framework",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
		Decisions: []string{"dec-framework", "dec-rsc"},
	}, "Body prose explaining the framework choice and the hiring-velocity argument."))

	return fs
}

// TestRunJustifyCommand_FanOutAgainstStrategy — full fan-out flow.
// Scripts: 1 splitter call + 2 per-decision (challenger + researcher
// + advocate each = 6 calls) + 1 synthesizer call = 8 LLM calls.
func TestRunJustifyCommand_FanOutAgainstStrategy(t *testing.T) {
	fs := fixtureFanOut(t)

	// Splitter routes the challenge to both decisions, no parent
	// prose component.
	splitterPayload := agent.ChallengeSplit{
		DecisionShards: []agent.DecisionShard{
			{DecisionID: "dec-framework", Shard: "Why all the overhead of NextJS?"},
			{DecisionID: "dec-rsc", Shard: "Do we need RSC when we anticipate NO SEO?"},
		},
		ParentProseShard: "",
		Rationale:        "challenge cleanly decomposes into framework choice and RSC choice",
	}

	// Per-decision response trios. Each per-decision flow runs:
	// challenger → researcher → advocate, in that order.
	frameworkChallenge := agent.ChallengeBrief{Concerns: []agent.AdversarialConcern{{
		Weakness:        "the framework choice doesn't justify the operational complexity",
		Evidence:        "the rationale cites RSC and Vercel deployment but the user explicitly rejects both",
		Counterproposal: "evaluate TanStack Start as a lighter alternative without server-default complexity",
	}}}
	frameworkResearch := agent.ResearchBrief{Findings: []agent.Finding{{
		Query: "TanStack Start production maturity 2026",
		Result: "TanStack Start is in beta as of Q1 2026 with limited production references.",
	}}}
	frameworkDefense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{
			Defense: "Next.js holds for hiring velocity even when RSC isn't load-bearing.",
		},
		Verdict:        "partially_held_up",
		BreakingPoints: []string{"the operational-complexity argument needs a real measurement"},
	}

	rscChallenge := agent.ChallengeBrief{Concerns: []agent.AdversarialConcern{{
		Weakness:        "RSC is overkill without SEO requirements",
		Evidence:        "the rationale cites bundle reduction but doesn't acknowledge that internal apps are bundle-tolerant",
		Counterproposal: "default to client components and opt into RSC only where data fetching demands it",
	}}}
	rscResearch := agent.ResearchBrief{Findings: []agent.Finding{{
		Query:  "RSC overhead for non-SEO internal applications",
		Result: "RSC adds boundary friction without compensating SEO benefit for internal-only apps.",
	}}}
	rscDefense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{
			Defense: "RSC's data-fetching pattern still helps even without SEO.",
		},
		Verdict:        "broke_down",
		BreakingPoints: []string{"the SEO justification doesn't apply; RSC complexity isn't paid for here"},
	}

	synthesisPayload := agent.SynthesisVerdict{
		Defense: "Strategy partially holds: framework choice is salvageable but RSC-by-default broke under the no-SEO constraint.",
		Verdict: "broke_down",
		BreakingPoints: []agent.StrategyBreakingPoint{
			{Description: "RSC-by-default isn't justified absent SEO", SourceDecision: "dec-rsc"},
			{Description: "operational complexity needs measurement", SourceDecision: "dec-framework"},
		},
		Rationale: "framework choice survives partially; RSC choice broke down outright",
	}

	mock := agent.NewMockExecutor(
		// Splitter
		agent.MockResponse{AgentID: "justify_splitter", Response: &agent.AgentOutput{Content: mustJSON(t, splitterPayload)}},
		// Per-decision flow for dec-framework: challenger, researcher, advocate
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, frameworkChallenge)}},
		agent.MockResponse{AgentID: "justify_researcher", Response: &agent.AgentOutput{Content: mustJSON(t, frameworkResearch)}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, frameworkDefense)}},
		// Per-decision flow for dec-rsc: challenger, researcher, advocate
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, rscChallenge)}},
		agent.MockResponse{AgentID: "justify_researcher", Response: &agent.AgentOutput{Content: mustJSON(t, rscResearch)}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, rscDefense)}},
		// Synthesis
		agent.MockResponse{AgentID: "justify_synthesizer", Response: &agent.AgentOutput{Content: mustJSON(t, synthesisPayload)}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "strat-frontend",
		"Why all the overhead of NextJS? Do we need RSC when we anticipate NO SEO?")
	require.NoError(t, err)
	require.NotNil(t, result.FanOut, "fan-out path must populate result.FanOut")
	assert.Nil(t, result.Adversarial, "decision-target single-flow output must not be populated for strategy targets")

	// Splitter classified into both decisions.
	require.NotNil(t, result.FanOut.Split)
	assert.Len(t, result.FanOut.Split.DecisionShards, 2)

	// Two per-decision results, both with adversarial defenses.
	require.Len(t, result.FanOut.PerDecisionResults, 2)
	frameworkResult := result.FanOut.PerDecisionResults[0]
	rscResult := result.FanOut.PerDecisionResults[1]
	assert.Equal(t, "dec-framework", frameworkResult.DecisionID)
	assert.Equal(t, "partially_held_up", frameworkResult.Adversarial.Verdict)
	assert.Equal(t, "dec-rsc", rscResult.DecisionID)
	assert.Equal(t, "broke_down", rscResult.Adversarial.Verdict)

	// Synthesis with strategy-level verdict + tagged breaking points.
	require.NotNil(t, result.FanOut.Synthesis)
	assert.Equal(t, "broke_down", result.FanOut.Synthesis.Verdict)
	require.Len(t, result.FanOut.Synthesis.BreakingPoints, 2)
	assert.Equal(t, "dec-rsc", result.FanOut.Synthesis.BreakingPoints[0].SourceDecision)

	// 8 LLM calls total: 1 splitter + 2x(challenger+researcher+advocate) + 1 synthesizer
	assert.Equal(t, 8, mock.CallCount(),
		"fan-out fires splitter + N*(challenger+researcher+advocate) + synthesizer")

	// Markdown surfaces both per-decision sub-sections, the
	// synthesis, and the per-source suggested-next-step routing.
	assert.Contains(t, result.Markdown, "# Justifying `strat-frontend` (fan-out)")
	assert.Contains(t, result.Markdown, "dec-framework")
	assert.Contains(t, result.Markdown, "dec-rsc")
	assert.Contains(t, result.Markdown, "Strategy-level synthesis")
	assert.Contains(t, result.Markdown, "Verdict: BROKE DOWN")
	assert.Contains(t, result.Markdown, "locutus refine dec-rsc --supersede",
		"strategy-level breaks must route to per-decision --supersede commands")
	assert.Contains(t, result.Markdown, "locutus refine dec-framework --supersede")
}

// TestRunJustifyCommand_FanOutSkipsIrrelevantShards — splitter
// emits an empty shard for one decision; the orchestrator must
// skip that decision rather than firing a wasted per-decision run.
func TestRunJustifyCommand_FanOutSkipsIrrelevantShards(t *testing.T) {
	fs := fixtureFanOut(t)

	// Splitter says only dec-rsc is relevant; dec-framework's
	// shard is empty.
	splitterPayload := agent.ChallengeSplit{
		DecisionShards: []agent.DecisionShard{
			{DecisionID: "dec-framework", Shard: ""},
			{DecisionID: "dec-rsc", Shard: "Do we need RSC when we anticipate NO SEO?"},
		},
		Rationale: "only RSC is challenged; framework choice not addressed",
	}

	rscChallenge := agent.ChallengeBrief{Concerns: []agent.AdversarialConcern{{
		Weakness: "RSC is overkill without SEO", Evidence: "the rationale cites bundle reduction but doesn't acknowledge internal-app bundle tolerance",
		Counterproposal: "default to client components and opt into RSC only where data fetching demands it",
	}}}
	rscResearch := agent.ResearchBrief{Findings: []agent.Finding{{Query: "x", Result: "y"}}}
	rscDefense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{Defense: "RSC defended on data-fetching grounds."},
		Verdict:            "held_up",
	}

	synthesisPayload := agent.SynthesisVerdict{
		Defense:   "Strategy holds; only one decision was contested and it survived.",
		Verdict:   "held_up",
		Rationale: "single-decision challenge resolved cleanly",
	}

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "justify_splitter", Response: &agent.AgentOutput{Content: mustJSON(t, splitterPayload)}},
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, rscChallenge)}},
		agent.MockResponse{AgentID: "justify_researcher", Response: &agent.AgentOutput{Content: mustJSON(t, rscResearch)}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, rscDefense)}},
		agent.MockResponse{AgentID: "justify_synthesizer", Response: &agent.AgentOutput{Content: mustJSON(t, synthesisPayload)}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "strat-frontend",
		"Do we need RSC when we anticipate NO SEO?")
	require.NoError(t, err)
	require.NotNil(t, result.FanOut)
	require.Len(t, result.FanOut.PerDecisionResults, 1,
		"empty-shard decisions must not get a per-decision run")
	assert.Equal(t, "dec-rsc", result.FanOut.PerDecisionResults[0].DecisionID)

	// 5 LLM calls: 1 splitter + 1*(challenger+researcher+advocate) + 1 synthesizer
	assert.Equal(t, 5, mock.CallCount(),
		"only the relevant decision triggers a per-decision flow")
}

// TestRunJustifyCommand_DecisionTargetUsesSingleFlow — when the
// target id is a decision, the existing single-target adversarial
// flow runs, NOT fan-out. Asserts the dispatch routing didn't
// regress under the new code paths.
func TestRunJustifyCommand_DecisionTargetUsesSingleFlow(t *testing.T) {
	fs := fixtureFanOut(t)

	challenge := agent.ChallengeBrief{Concerns: []agent.AdversarialConcern{{
		Weakness: "the framework choice locks us in", Evidence: "the rationale cites Vercel-shaped deployment as a benefit but creates a hard dependency",
		Counterproposal: "consider a more portable runtime and document the lock-in cost explicitly",
	}}}
	research := agent.ResearchBrief{Findings: []agent.Finding{{Query: "x", Result: "y"}}}
	defense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{Defense: "Held up on hiring grounds."},
		Verdict:            "held_up",
	}

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, challenge)}},
		agent.MockResponse{AgentID: "justify_researcher", Response: &agent.AgentOutput{Content: mustJSON(t, research)}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, defense)}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "dec-framework", "Why Next.js?")
	require.NoError(t, err)
	require.NotNil(t, result.Adversarial,
		"decision target must use the single-flow adversarial path")
	assert.Nil(t, result.FanOut, "fan-out output must be nil for decision targets")
	assert.Equal(t, "held_up", result.Adversarial.Verdict)
	assert.Equal(t, 3, mock.CallCount(),
		"single-flow path fires exactly 3 LLM calls (challenger + researcher + advocate)")
}
