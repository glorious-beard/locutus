package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustJSON encodes a value to JSON or fails the test. Used to script
// MockExecutor responses with the same shape RunInto expects.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestJustifySoloDispatch(t *testing.T) {
	fs := fixtureExplain(t)

	brief := agent.JustificationBrief{
		Defense:                     "This decision aligns with goal 1 and 2.",
		GoalClausesCited:            []string{"GOALS.md §1: build a thing"},
		ConditionsUnderWhichInvalid: []string{"if the team grows past 30 engineers"},
	}
	mock := agent.NewMockExecutor(
		agent.MockResponse{Response: &agent.AgentOutput{Content: mustJSON(t, brief), Model: "test"}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "dec-shared", "")
	require.NoError(t, err)

	assert.Equal(t, "dec-shared", result.ID)
	assert.Equal(t, "", result.Challenge)
	require.NotNil(t, result.Brief)
	assert.Equal(t, brief.Defense, result.Brief.Defense)
	assert.Nil(t, result.Adversarial)

	// Markdown carries the defense + cited goals.
	assert.Contains(t, result.Markdown, "# Justifying `dec-shared`")
	assert.Contains(t, result.Markdown, brief.Defense)
	assert.Contains(t, result.Markdown, "GOALS.md §1: build a thing")

	// Exactly one call, made against spec_advocate.
	calls := mock.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "spec_advocate", calls[0].Def.ID)
	assert.Equal(t, "JustificationBrief", calls[0].Def.OutputSchema)

	// User message includes the rendered explain output.
	require.Len(t, calls[0].Input.Messages, 1)
	user := calls[0].Input.Messages[0].Content
	assert.Contains(t, user, "## Node under review")
	assert.Contains(t, user, "dec-shared")
	assert.Contains(t, user, "Shared rationale text.")
}

func TestJustifyAdversarialDispatch(t *testing.T) {
	fs := fixtureExplain(t)

	challenge := agent.ChallengeBrief{
		Concerns: []agent.AdversarialConcern{{
			Weakness:        "vendor lock-in",
			Evidence:        "GOALS §4: cost discipline",
			Counterproposal: "self-host alternative",
		}},
	}
	research := agent.ResearchBrief{
		Findings: []agent.Finding{{
			Query:  "Does vendor X impose lock-in via proprietary APIs?",
			Result: "Vendor X publishes an OSS export tool and a documented schema; portability is real.",
		}},
	}
	defense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{
			Defense:                     "We choose this anyway because the operational savings outweigh the risk.",
			GoalClausesCited:            []string{"GOALS §4"},
			ConditionsUnderWhichInvalid: []string{"if vendor pricing increases >20%/yr"},
		},
		PointByPointAddressed: []agent.AddressedConcern{{
			ConcernSummary: "vendor lock-in",
			Response:       "Mitigated by exit clause; the OSS export tool surfaced in research confirms portability.",
			StillStands:    true,
		}},
		Verdict:        "held_up",
		BreakingPoints: nil,
	}

	// Tagged responses so order doesn't matter and we can verify the
	// challenger fires first, then researcher, then advocate.
	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, challenge), Model: "test"}},
		agent.MockResponse{AgentID: "researcher", Response: &agent.AgentOutput{Content: mustJSON(t, research), Model: "test"}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, defense), Model: "test"}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "dec-shared", "What about vendor lock-in?")
	require.NoError(t, err)

	require.NotNil(t, result.Challenger)
	require.NotNil(t, result.Research)
	require.NotNil(t, result.Adversarial)
	assert.Nil(t, result.Brief)
	assert.Equal(t, "held_up", result.Adversarial.Verdict)
	assert.Equal(t, "vendor lock-in", result.Challenger.Concerns[0].Weakness)
	require.Len(t, result.Research.Findings, 1)
	assert.Contains(t, result.Research.Findings[0].Result, "OSS export tool")

	// Markdown surfaces challenge, concerns, research findings, advocate response, verdict.
	assert.Contains(t, result.Markdown, "**Challenge:** What about vendor lock-in?")
	assert.Contains(t, result.Markdown, "vendor lock-in")
	assert.Contains(t, result.Markdown, "## Researcher's findings")
	assert.Contains(t, result.Markdown, "OSS export tool")
	assert.Contains(t, result.Markdown, "Mitigated by exit clause")
	assert.Contains(t, result.Markdown, "Verdict: HELD UP")

	// Three calls fired in order: challenger, researcher, advocate.
	calls := mock.Calls()
	require.Len(t, calls, 3)
	assert.Equal(t, "spec_challenger", calls[0].Def.ID)
	assert.Equal(t, "researcher", calls[1].Def.ID)
	assert.Equal(t, "spec_advocate", calls[2].Def.ID)
	assert.Equal(t, "ResearchBrief", calls[1].Def.OutputSchema)
	assert.Equal(t, "AdversarialDefense", calls[2].Def.OutputSchema)

	// Researcher must have grounding enabled so its findings are
	// drawn from current sources, not training data.
	assert.True(t, calls[1].Def.Grounding, "researcher must run grounded")

	// Researcher's user prompt includes the challenger's concerns.
	researcherUser := calls[1].Input.Messages[0].Content
	assert.Contains(t, researcherUser, "## Concerns to investigate")
	assert.Contains(t, researcherUser, "vendor lock-in")

	// Advocate's user prompt includes both challenger's brief and
	// researcher's findings.
	advocateUser := calls[2].Input.Messages[0].Content
	assert.Contains(t, advocateUser, "## Challenger's concerns")
	assert.Contains(t, advocateUser, "vendor lock-in")
	assert.Contains(t, advocateUser, "## Researcher's findings")
	assert.Contains(t, advocateUser, "OSS export tool")
	assert.Contains(t, advocateUser, "## Challenge from user")
}

func TestJustifyAdversarialBrokenDownSurfacesBreakingPoints(t *testing.T) {
	fs := fixtureExplain(t)

	challenge := agent.ChallengeBrief{
		Concerns: []agent.AdversarialConcern{{
			Weakness: "scale assumptions wrong", Evidence: "evidence", Counterproposal: "use sharded approach",
		}},
	}
	research := agent.ResearchBrief{Findings: []agent.Finding{{Query: "scale", Result: "evidence shows the chosen path saturates at 10k QPS"}}}
	defense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{Defense: "Concession: the approach doesn't hold at this scale."},
		PointByPointAddressed: []agent.AddressedConcern{{
			ConcernSummary: "scale", Response: "agreed", StillStands: false,
		}},
		Verdict:        "broke_down",
		BreakingPoints: []string{"need sharded write path"},
	}
	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, challenge), Model: "test"}},
		agent.MockResponse{AgentID: "researcher", Response: &agent.AgentOutput{Content: mustJSON(t, research), Model: "test"}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, defense), Model: "test"}},
	)

	result, err := RunJustifyCommand(context.Background(), mock, fs, "dec-shared", "Does this scale?")
	require.NoError(t, err)

	assert.Equal(t, "broke_down", result.Adversarial.Verdict)
	assert.Contains(t, result.Markdown, "Verdict: BROKE DOWN")
	assert.Contains(t, result.Markdown, "need sharded write path")
	assert.Contains(t, result.Markdown, "## Suggested next step")
	assert.Contains(t, result.Markdown, "locutus refine dec-shared --brief")
}

func TestJustifyInvalidVerdictRejected(t *testing.T) {
	fs := fixtureExplain(t)
	challenge := agent.ChallengeBrief{Concerns: []agent.AdversarialConcern{{Weakness: "x"}}}
	research := agent.ResearchBrief{Findings: []agent.Finding{{Query: "x", Result: "y"}}}
	defense := agent.AdversarialDefense{
		JustificationBrief: agent.JustificationBrief{Defense: "x"},
		Verdict:            "maybe", // invalid
	}
	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: mustJSON(t, challenge), Model: "test"}},
		agent.MockResponse{AgentID: "researcher", Response: &agent.AgentOutput{Content: mustJSON(t, research), Model: "test"}},
		agent.MockResponse{AgentID: "spec_advocate", Response: &agent.AgentOutput{Content: mustJSON(t, defense), Model: "test"}},
	)

	_, err := RunJustifyCommand(context.Background(), mock, fs, "dec-shared", "challenge")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid verdict")
}

func TestJustifyEmptyChallengerConcerns(t *testing.T) {
	fs := fixtureExplain(t)
	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_challenger", Response: &agent.AgentOutput{Content: `{"concerns":[]}`, Model: "test"}},
	)
	_, err := RunJustifyCommand(context.Background(), mock, fs, "dec-shared", "challenge")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no concerns")
}
