// DJ-125 Phase 7 — scout grading tests.

package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScoutBriefSchemaCarriesConcernDispositions confirms the
// reflected schema includes the ConcernDispositions field with the
// enum-shape values DJ-125 Phase 7 needs.
func TestScoutBriefSchemaCarriesConcernDispositions(t *testing.T) {
	schema, err := SchemaFor("ScoutBrief")
	require.NoError(t, err, "ScoutBrief schema must reflect cleanly")

	encoded, err := json.Marshal(schema)
	require.NoError(t, err)
	s := string(encoded)
	assert.Contains(t, s, "concern_dispositions")
	assert.Contains(t, s, "addressed")
	assert.Contains(t, s, "wontfix")
	assert.Contains(t, s, "still_open")
}

// TestMergeScoutBriefAppliesConcernDispositions verifies the merge
// applies addressed/wontfix to state.Concerns by manifest id and
// captures justifications.
func TestMergeScoutBriefAppliesConcernDispositions(t *testing.T) {
	state := &PlanningState{
		Concerns: []Concern{
			{AgentID: "architect_critic", Text: "concern zero", Status: ConcernStatusOpen},
			{AgentID: "devops_critic", Text: "concern one", Status: ConcernStatusOpen},
			{AgentID: "cost_critic", Text: "concern two", Status: ConcernStatusOpen},
		},
	}
	brief := ScoutBrief{
		Converged: false,
		ConcernDispositions: []ConcernDisposition{
			{ConcernID: "c-0", Disposition: "addressed", Justification: "Resolved by dec-new"},
			{ConcernID: "c-1", Disposition: "wontfix", Justification: "Accepted tradeoff"},
			{ConcernID: "c-2", Disposition: "still_open", Justification: "Gap still in feat-x"},
		},
	}
	briefJSON, err := json.Marshal(brief)
	require.NoError(t, err)

	mergeScoutBrief(state, []RoundResult{{AgentID: "spec_scout", Output: string(briefJSON)}})

	assert.Equal(t, ConcernStatusAddressed, state.Concerns[0].Status)
	assert.Equal(t, "Resolved by dec-new", state.Concerns[0].Justification)
	assert.Equal(t, ConcernStatusWontfix, state.Concerns[1].Status)
	assert.Equal(t, "Accepted tradeoff", state.Concerns[1].Justification)
	assert.Equal(t, ConcernStatusOpen, state.Concerns[2].Status,
		"still_open disposition leaves status as open")
	assert.Equal(t, "Gap still in feat-x", state.Concerns[2].Justification,
		"still_open disposition records the justification so next iter sees it")
}

// TestMergeScoutBriefSkipsUnknownConcernID verifies a disposition
// referencing a concern id outside state.Concerns is logged and
// skipped, leaving the rest applied.
func TestMergeScoutBriefSkipsUnknownConcernID(t *testing.T) {
	state := &PlanningState{
		Concerns: []Concern{
			{AgentID: "architect_critic", Text: "concern zero", Status: ConcernStatusOpen},
		},
	}
	brief := ScoutBrief{
		ConcernDispositions: []ConcernDisposition{
			{ConcernID: "c-99", Disposition: "addressed", Justification: "out of range"},
			{ConcernID: "c-0", Disposition: "addressed", Justification: "in range"},
		},
	}
	briefJSON, _ := json.Marshal(brief)
	mergeScoutBrief(state, []RoundResult{{AgentID: "spec_scout", Output: string(briefJSON)}})

	assert.Equal(t, ConcernStatusAddressed, state.Concerns[0].Status)
	assert.Equal(t, "in range", state.Concerns[0].Justification)
}

// TestScoutConvergenceRejectsConvergedWithOpenConcerns verifies the
// scoutSpawnFor closure refuses to terminate when brief.Converged is
// true but open concerns remain after dispositions land.
func TestScoutConvergenceRejectsConvergedWithOpenConcerns(t *testing.T) {
	state := PlanningState{
		Concerns: []Concern{
			{Status: ConcernStatusOpen, Text: "remains open"},
		},
	}
	brief := ScoutBrief{Converged: true}
	briefJSON, _ := json.Marshal(brief)
	results := []RoundResult{{AgentID: "spec_scout", Output: string(briefJSON)}}
	snap := StateSnapshot[PlanningState]{State: state}

	spawn := scoutSpawnFor(0, 5, nil, nil)
	_, _, err := spawn(nil, snap, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 concern(s) remain open")
}

// TestScoutConvergenceAcceptsConvergedAfterDispositions verifies that
// when every open concern was disposed (addressed/wontfix/stale) and
// brief.Converged is true, the spawner returns the terminate signal
// (nil steps, nil edges, nil err).
func TestScoutConvergenceAcceptsConvergedAfterDispositions(t *testing.T) {
	state := PlanningState{
		Concerns: []Concern{
			{Status: ConcernStatusAddressed, Text: "done"},
			{Status: ConcernStatusStale, Text: "stale"},
		},
	}
	brief := ScoutBrief{Converged: true}
	briefJSON, _ := json.Marshal(brief)
	results := []RoundResult{{AgentID: "spec_scout", Output: string(briefJSON)}}
	snap := StateSnapshot[PlanningState]{State: state}

	spawn := scoutSpawnFor(0, 5, nil, nil)
	steps, edges, err := spawn(nil, snap, results)
	require.NoError(t, err)
	assert.Nil(t, steps)
	assert.Nil(t, edges)
}

// TestScoutPromptDescribesConcernGrading reads the scaffolded
// spec_scout.md prompt and asserts it teaches concern_dispositions
// with all four disposition kinds in scope.
func TestScoutPromptDescribesConcernGrading(t *testing.T) {
	body, err := os.ReadFile("../scaffold/agents/spec_scout.md")
	require.NoError(t, err)
	text := strings.ToLower(string(body))
	assert.Contains(t, text, "concern_dispositions")
	assert.Contains(t, text, "addressed")
	assert.Contains(t, text, "wontfix")
	assert.Contains(t, text, "still_open")
	assert.Contains(t, text, "stale", "the prompt must mention stale so the scout knows the mechanical pre-pass owns it")
}
