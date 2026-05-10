package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// marshalJSON is a thin require-friendly wrapper used by the
// orchestrator integration tests below to script mock responses.
func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

// TestDegenerateChallengerBrief_DummyTokens — the canonical case
// observed in winplan: the model emits a single concern with every
// field set to "dummy". Triggers the placeholder-token branch.
func TestDegenerateChallengerBrief_DummyTokens(t *testing.T) {
	b := &ChallengeBrief{
		Concerns: []AdversarialConcern{
			{Weakness: "dummy", Evidence: "dummy", Counterproposal: "dummy"},
		},
	}
	reason, degenerate := degenerateChallengerBrief(b)
	require.True(t, degenerate)
	assert.Contains(t, reason, "placeholder")
}

// TestDegenerateChallengerBrief_PlaceholderVariants — the validator
// should catch the common-suspect placeholder vocabulary, lowercased,
// trimmed.
func TestDegenerateChallengerBrief_PlaceholderVariants(t *testing.T) {
	for _, tok := range []string{"DUMMY", " placeholder ", "TODO", "tbd", "Foo", "bar", "lorem", "ipsum", "n/a", "NONE", "..."} {
		b := &ChallengeBrief{
			Concerns: []AdversarialConcern{
				{Weakness: tok, Evidence: "real evidence with substance", Counterproposal: "real counterproposal here"},
			},
		}
		_, degenerate := degenerateChallengerBrief(b)
		assert.True(t, degenerate, "expected %q to trigger placeholder detection", tok)
	}
}

// TestDegenerateChallengerBrief_TooShort — fields under
// minChallengerFieldLen runes are flagged as degenerate even if not
// in the placeholder vocabulary. A real challenger writes at least a
// short sentence per field.
func TestDegenerateChallengerBrief_TooShort(t *testing.T) {
	b := &ChallengeBrief{
		Concerns: []AdversarialConcern{
			{Weakness: "short", Evidence: "evidence with substance and more text", Counterproposal: "counterproposal with substance and more"},
		},
	}
	reason, degenerate := degenerateChallengerBrief(b)
	require.True(t, degenerate)
	assert.Contains(t, reason, "shorter than")
	assert.Contains(t, reason, "weakness")
}

// TestDegenerateChallengerBrief_AllDuplicates — when every concern is
// an exact duplicate of the first, the model is producing minimum-
// JSON output rather than enumerating distinct concerns.
func TestDegenerateChallengerBrief_AllDuplicates(t *testing.T) {
	concern := AdversarialConcern{
		Weakness:        "the spec doesn't address authentication boundaries clearly",
		Evidence:        "GOALS.md line 14 calls out auth as a top-level requirement",
		Counterproposal: "introduce a dedicated auth strategy with per-role boundaries",
	}
	b := &ChallengeBrief{
		Concerns: []AdversarialConcern{concern, concern, concern},
	}
	reason, degenerate := degenerateChallengerBrief(b)
	require.True(t, degenerate)
	assert.Contains(t, reason, "duplicates")
}

// TestDegenerateChallengerBrief_HealthyBrief — a real-shaped brief
// with substantive distinct concerns is not flagged. Mirrors the
// shape of the first (working) winplan run's challenger output.
func TestDegenerateChallengerBrief_HealthyBrief(t *testing.T) {
	b := &ChallengeBrief{
		Concerns: []AdversarialConcern{
			{
				Weakness:        "The RSC/SSR rationale is a solution looking for a problem in a pure authenticated application",
				Evidence:        "The spec's own justification for RSCs is data security and avoiding excessive client-side logic, not SEO or TTFB on public pages",
				Counterproposal: "Audit the actual RSC usage planned for feat-voter-file-management; if the primary benefit is avoiding raw data endpoints, that concern is better addressed at the API/auth layer",
			},
			{
				Weakness:        "The 'compatibility with GCP Cloud Run' framing treats Next.js standalone mode as a feature rather than acknowledging it as an operational liability",
				Evidence:        "strat-compute-platform specifies GCP Cloud Run, which bills per-request and has cold-start sensitivity",
				Counterproposal: "Benchmark Next.js App Router on GCP Cloud Run against a TanStack Start SPA deployment",
			},
		},
	}
	reason, degenerate := degenerateChallengerBrief(b)
	assert.False(t, degenerate, "healthy brief should not be flagged (reason: %q)", reason)
}

// TestDegenerateChallengerBrief_NilOrEmpty — a nil brief or an empty
// concerns slice is NOT degenerate; that's a separate "no concerns"
// state handled by the existing len(Concerns) == 0 check upstream.
func TestDegenerateChallengerBrief_NilOrEmpty(t *testing.T) {
	_, degenerate := degenerateChallengerBrief(nil)
	assert.False(t, degenerate, "nil brief is empty, not degenerate")

	_, degenerate = degenerateChallengerBrief(&ChallengeBrief{})
	assert.False(t, degenerate, "zero-concerns brief is empty, not degenerate")
}

// TestRunJustifyAgainst_FailsLoudOnDegenerateChallenger — the
// orchestrator must surface the degenerate-output condition with a
// clear error message rather than feeding the dummy brief to the
// researcher and advocate. The challenger output IS returned so the
// cmd layer can render it for the user to inspect. The retry layer
// is configured for challengerMaxAttempts dispatches, so we provide
// that many degenerate responses to exhaust the loop.
func TestRunJustifyAgainst_FailsLoudOnDegenerateChallenger(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{
		Content: `{"concerns":[{"weakness":"dummy","evidence":"dummy","counterproposal":"dummy"}]}`,
	}}
	scripts := make([]MockResponse, challengerMaxAttempts)
	for i := range scripts {
		scripts[i] = dummy
	}
	mock := NewMockExecutor(scripts...)

	in := JustifyInputs{
		NodeID:       "feat-x",
		NodeMarkdown: "# feat-x\n\nbody",
		GoalsBody:    "goals",
		Challenge:    "why",
	}

	challenge, research, defense, err := RunJustifyAgainst(context.Background(), mock, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "degenerate")
	assert.Contains(t, err.Error(), "feat-x")
	assert.Contains(t, err.Error(), "re-run")

	// The orchestrator should NOT have called the researcher or
	// advocate. The mock script length matches the retry cap; any
	// downstream dispatch would exhaust it and surface differently.
	require.NotNil(t, challenge, "challenger output must be returned so the cmd layer can show what the model produced")
	assert.Len(t, challenge.Concerns, 1)
	assert.Equal(t, "dummy", challenge.Concerns[0].Weakness)
	assert.Nil(t, research, "researcher must not run when challenger output is degenerate")
	assert.Nil(t, defense, "advocate must not run when challenger output is degenerate")
	assert.Equal(t, challengerMaxAttempts, mock.CallCount(),
		"orchestrator must exhaust the retry budget before failing")
}

// TestRunJustifyAgainst_RetriesAfterDegenerateChallenger — the
// realistic Sonnet 4.6 schema-skeleton failure pattern: first attempt
// returns dummy placeholders, second attempt produces a real critique.
// The orchestrator must (a) detect the degenerate first attempt,
// (b) re-dispatch the challenger, (c) accept the second-attempt
// brief, and (d) carry on to researcher and advocate normally.
func TestRunJustifyAgainst_RetriesAfterDegenerateChallenger(t *testing.T) {
	realChallenge := ChallengeBrief{
		Concerns: []AdversarialConcern{{
			Weakness:        "the chosen path bakes in vendor lock-in via proprietary APIs",
			Evidence:        "GOALS §4 calls out cost discipline; switching costs from a sole-source vendor erode that posture",
			Counterproposal: "self-host the equivalent OSS alternative or adopt a vendor with documented data export",
		}},
	}
	realChallengeJSON := marshalJSON(t, realChallenge)
	research := ResearchBrief{Findings: []Finding{{Query: "lock-in", Result: "documented export tool exists"}}}
	researchJSON := marshalJSON(t, research)
	defense := AdversarialDefense{
		JustificationBrief: JustificationBrief{Defense: "We accept the trade for operational simplicity."},
		PointByPointAddressed: []AddressedConcern{{
			ConcernSummary: "lock-in",
			Response:       "Mitigated by OSS export tool.",
			StillStands:    true,
		}},
		Verdict: "held_up",
	}
	defenseJSON := marshalJSON(t, defense)

	mock := NewMockExecutor(
		// First challenger attempt: degenerate.
		MockResponse{Response: &AgentOutput{Content: `{"concerns":[{"weakness":"dummy","evidence":"dummy","counterproposal":"dummy"}]}`}},
		// Retry: substantive concern.
		MockResponse{Response: &AgentOutput{Content: realChallengeJSON}},
		MockResponse{Response: &AgentOutput{Content: researchJSON}},
		MockResponse{Response: &AgentOutput{Content: defenseJSON}},
	)

	in := JustifyInputs{
		NodeID:       "dec-shared",
		NodeMarkdown: "# dec-shared\n\nbody",
		GoalsBody:    "goals",
		Challenge:    "what about lock-in",
	}

	challenge, researchOut, defenseOut, err := RunJustifyAgainst(context.Background(), mock, in)
	require.NoError(t, err, "the second challenger attempt produced a real brief; orchestrator must accept and continue")

	require.NotNil(t, challenge)
	require.Len(t, challenge.Concerns, 1)
	assert.Contains(t, challenge.Concerns[0].Weakness, "vendor lock-in",
		"the orchestrator must use the second (substantive) attempt's brief, not the first (degenerate) one")

	require.NotNil(t, researchOut)
	require.NotNil(t, defenseOut)
	assert.Equal(t, "held_up", defenseOut.Verdict)

	assert.Equal(t, 4, mock.CallCount(),
		"two challenger attempts + researcher + advocate = four dispatches")
}

// Test moved to internal/scaffold/agents_test.go after the prompt
// extraction (advocate prompt now lives in
// internal/scaffold/agents/spec_advocate.md). The grounding-discipline
// invariant is enforced there against the embedded scaffold copy.
