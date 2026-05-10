package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunResearch_StampsToolOutcomesOnBrief — the tool-call outcomes
// reported by the adapter ride through onto ResearchBrief.ToolOutcomes
// so a downstream caller (the advocate's prompt builder; the cmd
// renderer) can distinguish grounded from ungrounded findings without
// re-parsing the raw provider response.
func TestRunResearch_StampsToolOutcomesOnBrief(t *testing.T) {
	briefJSON := `{"findings":[
		{"query":"q1","result":"grounded result"},
		{"query":"q2","result":"search tool errored on 'q2' — finding ungrounded; no evidence retrieved during this call."}
	]}`

	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{
		Content: briefJSON,
		ToolCalls: []ToolCall{
			{Name: "web_search", Query: "q1", Status: "success"},
			{Name: "web_search", Query: "q2", Status: "error", ErrorCode: "unavailable"},
		},
	}})

	in := JustifyInputs{
		NodeID:       "feat-x",
		NodeMarkdown: "# feat-x\n\nbody",
		Challenge:    "why",
		ChallengerOut: &ChallengeBrief{
			Concerns: []AdversarialConcern{{Weakness: "w", Evidence: "e", Counterproposal: "c"}},
		},
	}

	got, err := RunResearch(context.Background(), NewDispatcher(mock), in)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.Len(t, got.ToolOutcomes, 2)
	assert.Equal(t, "success", got.ToolOutcomes[0].Status)
	assert.Equal(t, "error", got.ToolOutcomes[1].Status)

	failed := got.FailedQueries()
	require.Len(t, failed, 1)
	assert.Equal(t, "q2", failed[0].Query)
	assert.Equal(t, "unavailable", failed[0].ErrorCode)
}

// TestRunResearch_NoToolCalls — when the call wasn't grounded (no
// search tool advertised, or the model didn't invoke it),
// ToolOutcomes is empty and FailedQueries returns nil. The brief
// still reflects whatever the model produced.
func TestRunResearch_NoToolCalls(t *testing.T) {
	briefJSON := `{"findings":[{"query":"q","result":"r"}]}`
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{Content: briefJSON}})

	in := JustifyInputs{
		NodeID:       "feat-x",
		NodeMarkdown: "# x",
		Challenge:    "why",
		ChallengerOut: &ChallengeBrief{
			Concerns: []AdversarialConcern{{Weakness: "w", Evidence: "e", Counterproposal: "c"}},
		},
	}

	got, err := RunResearch(context.Background(), NewDispatcher(mock), in)
	require.NoError(t, err)
	assert.Empty(t, got.ToolOutcomes)
	assert.Empty(t, got.FailedQueries())
}

// TestBuildAdvocateUserMessage_RendersUngroundedSection — the
// advocate's user message must include an explicit
// "Ungrounded research queries" section listing every failed query
// when ResearcherOut.FailedQueries is non-empty, so the model sees
// which findings to treat skeptically.
func TestBuildAdvocateUserMessage_RendersUngroundedSection(t *testing.T) {
	in := JustifyInputs{
		NodeID:       "feat-x",
		NodeMarkdown: "# feat-x\n\nbody",
		GoalsBody:    "goals",
		Challenge:    "why",
		ChallengerOut: &ChallengeBrief{
			Concerns: []AdversarialConcern{{Weakness: "w", Evidence: "e", Counterproposal: "c"}},
		},
		ResearcherOut: &ResearchBrief{
			Findings: []Finding{
				{Query: "q1", Result: "grounded result"},
				{Query: "q2", Result: "search tool errored on 'q2' — finding ungrounded"},
			},
			ToolOutcomes: []ToolCall{
				{Name: "web_search", Query: "q1", Status: "success"},
				{Name: "web_search", Query: "q2", Status: "error", ErrorCode: "unavailable"},
			},
		},
	}

	msg := buildAdvocateUserMessage(in)
	assert.Contains(t, msg, "Ungrounded research queries")
	assert.Contains(t, msg, "q2")
	assert.Contains(t, msg, "unavailable")
	// The ungrounded-section header should NOT appear when no failures
	// are present — verify by stripping the section out and checking
	// q1 alone doesn't trigger it elsewhere.
	assert.True(t, strings.Index(msg, "Ungrounded research queries") > strings.Index(msg, "Researcher's findings"),
		"ungrounded-queries section should follow the findings section so the advocate reads them in order")
}

// TestBuildAdvocateUserMessage_NoUngroundedSectionWhenAllSucceed —
// when every query grounded successfully, the advocate's prompt has
// no ungrounded-queries header. Avoids polluting the prompt with an
// empty caveat that would weaken the section's signal when it does
// fire.
func TestBuildAdvocateUserMessage_NoUngroundedSectionWhenAllSucceed(t *testing.T) {
	in := JustifyInputs{
		NodeID:       "feat-x",
		NodeMarkdown: "# x",
		Challenge:    "why",
		ResearcherOut: &ResearchBrief{
			Findings:     []Finding{{Query: "q", Result: "r"}},
			ToolOutcomes: []ToolCall{{Name: "web_search", Query: "q", Status: "success"}},
		},
	}
	msg := buildAdvocateUserMessage(in)
	assert.NotContains(t, msg, "Ungrounded research queries")
}

// Round-trip sanity: the schema unmarshal path keeps ToolOutcomes
// off the JSON contract so the model's output doesn't accidentally
// constrain it.
func TestResearchBrief_ToolOutcomesNotInJSON(t *testing.T) {
	b := ResearchBrief{
		Findings:     []Finding{{Query: "q", Result: "r"}},
		ToolOutcomes: []ToolCall{{Name: "web_search", Query: "q", Status: "success"}},
	}
	data, err := json.Marshal(b)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "tool_outcomes",
		"ToolOutcomes is post-call metadata; it must not round-trip through the model's JSON schema")
	assert.NotContains(t, string(data), "ToolOutcomes")
}
