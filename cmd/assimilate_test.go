package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssimilateFS(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := specio.NewMemFS()

	// Spec directories.
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)
	fs.MkdirAll(".borg/spec/decisions", 0o755)
	fs.MkdirAll(".borg/spec/strategies", 0o755)
	fs.WriteFile(".borg/spec/traces.json", []byte(`{"entries":{}}`), 0o644)

	// Assimilation agents and workflows.
	fs.MkdirAll(".borg/agents", 0o755)
	fs.MkdirAll(".borg/workflows", 0o755)
	agents := []string{"scout", "backend_analyzer", "frontend_analyzer", "infra_analyzer", "gap_analyst", "remediator"}
	for _, id := range agents {
		content := "---\nid: " + id + "\nrole: " + id + "\n---\nYou are the " + id + ".\n"
		fs.WriteFile(".borg/agents/"+id+".md", []byte(content), 0o644)
	}

	// Assimilation workflow matching embedded workflow.yaml — remediate
	// is NOT a workflow round; it runs as a separate pass in
	// cmd/assimilate after Analyze (Round 5 / DJ-045).
	fs.WriteFile(".borg/workflows/assimilation.yaml", []byte(`rounds:
  - id: scan
    agent: scout
    parallel: false
  - id: analyze
    agents: [backend_analyzer, frontend_analyzer, infra_analyzer]
    parallel: true
    depends_on: [scan]
  - id: gaps
    agent: gap_analyst
    parallel: false
    depends_on: [analyze]
max_rounds: 1
`), 0o644)

	// Synthetic codebase.
	fs.WriteFile("go.mod", []byte("module example.com/app\ngo 1.22\n"), 0o644)
	fs.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644)

	return fs
}

func mockAssimilationLLM() *agent.MockLLM {
	return agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"languages":["go"],"frameworks":[],"structure":"single-binary"}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-go","title":"Go backend","status":"inferred","confidence":0.95}],"entities":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[],"strategies":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"gaps":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-testing","title":"Add tests","status":"assumed"}],"features":[]}`}},
	)
}

func TestRunAssimilateProducesSpec(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLM()

	result, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	assert.NotEmpty(t, result.Decisions, "should produce decisions")
}

// TestRunAssimilateBridgesEventsToSink confirms the assimilation
// workflow forwards every agent's started/completed event to the
// supplied EventSink and calls Close on completion. Mirrors the
// equivalent test for the spec-generation council; both share the
// same WorkflowExecutor plumbing, but each constructs the sink
// bridge in its own entry point so a regression in one wouldn't
// surface in the other.
func TestRunAssimilateBridgesEventsToSink(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLM()

	sink := &agent.CapturingSink{}
	_, err := RunAssimilate(context.Background(), llm, fs, false, sink)
	require.NoError(t, err)

	events := sink.Events()
	require.NotEmpty(t, events,
		"sink should have received at least one workflow event from the assimilation council")

	var agentStarted, agentCompleted int
	for _, e := range events {
		if e.AgentID == "" {
			continue
		}
		switch e.Status {
		case "started":
			agentStarted++
		case "completed":
			agentCompleted++
		}
	}
	assert.Equal(t, agentStarted, agentCompleted,
		"every agent started should pair with a completed in a clean run")
	assert.Greater(t, agentStarted, 0, "at least one agent should have run")
	assert.True(t, sink.Closed(),
		"RunAssimilate must call sink.Close() after the run finishes")
}

func TestRunAssimilateMissingConfig(t *testing.T) {
	fs := specio.NewMemFS()
	llm := agent.NewMockLLM()

	result, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- Round 1 acceptance tests ---

// mockPersistenceLLM returns scripted responses that exercise the full
// scout → analyze → gap → remediate flow with persistable content (one
// feature + one decision). Used by the persistence tests below; kept
// separate from mockAssimilationLLM so the assertions are explicit.
func mockPersistenceLLM() *agent.MockLLM {
	return agent.NewMockLLM(
		// scout
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"languages":["go"]}`}},
		// analyze × 3 (backend, frontend, infra) — put the payload in the first one.
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{
  "features":[{"id":"feat-admin","title":"Admin console","status":"inferred","description":"Observed admin routes in handlers."}],
  "decisions":[{"id":"dec-go","title":"Go backend","status":"inferred","confidence":0.95,"rationale":"go.mod present"}]
}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
		// gap
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"gaps":[]}`}},
		// remediate
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
	)
}

// TestRunAssimilatePersistsInferredSpec is the core Round 1 acceptance
// test: the pipeline's output must land on disk under .borg/spec/ after
// a non-dry-run invocation.
func TestRunAssimilatePersistsInferredSpec(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockPersistenceLLM()

	result, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Feature file landed.
	featData, err := fs.ReadFile(".borg/spec/features/feat-admin.json")
	require.NoError(t, err, "feature JSON must be written to .borg/spec/features/")
	assert.Contains(t, string(featData), "feat-admin")

	// Decision file landed.
	decData, err := fs.ReadFile(".borg/spec/decisions/dec-go.json")
	require.NoError(t, err, "decision JSON must be written to .borg/spec/decisions/")
	assert.Contains(t, string(decData), "dec-go")
}

// TestRunAssimilateDryRunDoesNotWrite confirms the readOnlyFS wrapper
// still discards writes once Round 1 adds real persistence — the
// existing dry-run semantic must not regress.
func TestRunAssimilateDryRunDoesNotWrite(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockPersistenceLLM()

	ro := newReadOnlyFS(fs)
	_, err := RunAssimilate(context.Background(), llm, ro, false, nil)
	require.NoError(t, err)

	// Reads go to the underlying fs; but writes should have been dropped.
	_, err = fs.ReadFile(".borg/spec/features/feat-admin.json")
	assert.Error(t, err, "dry-run must not create new feature file")
	_, err = fs.ReadFile(".borg/spec/decisions/dec-go.json")
	assert.Error(t, err, "dry-run must not create new decision file")
}

// TestRunAssimilateRespectsExistingSpec verifies the pre-load invariant:
// the pipeline receives existing spec as context so the LLM can
// distinguish "new" from "enhancement of existing". We pin this by
// pre-seeding a feature and asserting the scout agent's prompt mentions
// the existing feature ID.
func TestRunAssimilateRespectsExistingSpec(t *testing.T) {
	fs := setupAssimilateFS(t)

	// Pre-seed an existing feature the assimilator should be told about.
	existing := spec.Feature{
		ID: "feat-auth", Title: "Auth", Status: spec.FeatureStatusActive,
		Description: "Existing authentication feature, hand-authored.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", existing, existing.Description))

	llm := mockPersistenceLLM()
	_, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	// Inspect the first prompt sent to the LLM (scout round). Must mention
	// the existing feature so the scout knows what's already in scope.
	calls := llm.Calls()
	require.NotEmpty(t, calls, "LLM must have been invoked")
	firstPrompt := callToPromptString(calls[0].Request)
	assert.Contains(t, firstPrompt, "feat-auth",
		"scout agent prompt must include existing spec context (feat-auth)")
}

// TestRunAssimilateUpdatesExistingNode confirms that when the LLM
// output matches an existing spec node's ID, the file is rewritten
// (not duplicated under a new name).
func TestRunAssimilateUpdatesExistingNode(t *testing.T) {
	fs := setupAssimilateFS(t)

	// Pre-seed with an intentionally stale title so we can tell whether
	// the update landed.
	stale := spec.Decision{
		ID: "dec-go", Title: "OLD title", Status: spec.DecisionStatusActive, Confidence: 0.5,
		Rationale: "stale",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-go", stale, "stale body"))

	llm := mockPersistenceLLM()
	_, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	data, err := fs.ReadFile(".borg/spec/decisions/dec-go.json")
	require.NoError(t, err)
	var updated spec.Decision
	require.NoError(t, json.Unmarshal(data, &updated))
	assert.Equal(t, "Go backend", updated.Title, "existing decision title must be updated by assimilate")
	assert.NotContains(t, strings.ToLower(string(data)), "old title",
		"stale content must be gone after assimilate rewrites the file")
}

// TestRunAssimilateSentinelCleanup verifies the .assimilating sentinel
// is written at start and removed on success. If a future crash leaves
// it in place, a later run can detect unfinished prior work.
func TestRunAssimilateSentinelCleanup(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockPersistenceLLM()

	_, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	_, err = fs.ReadFile(".borg/spec/.assimilating")
	assert.Error(t, err, "sentinel must be cleaned up after successful run")
}

// TestRunAssimilateInferredStatusDefault confirms that any LLM output
// missing a status field defaults to `inferred` for Features and
// Decisions landed via assimilate (DJ-019 posture: heuristic-derived
// starts as `inferred`).
func TestRunAssimilateInferredStatusDefault(t *testing.T) {
	fs := setupAssimilateFS(t)

	// LLM returns a Feature without a status field.
	llm := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"features":[{"id":"feat-nostatus","title":"Mystery"}]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"gaps":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{}`}},
	)

	_, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	data, err := fs.ReadFile(".borg/spec/features/feat-nostatus.json")
	require.NoError(t, err)
	var got spec.Feature
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, spec.FeatureStatusInferred, got.Status,
		"Features landed via assimilate without explicit status must default to inferred")
}

// callToPromptString flattens a GenerateRequest into a single string so
// tests can assert on prompt contents regardless of how the orchestrator
// split the content across messages.
func callToPromptString(req agent.GenerateRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// mockAssimilationLLMWithGaps scripts an analysis pipeline whose
// gap_analyst surfaces a non-empty gap, plus a remediator response that
// emits an assumed Decision filling that gap. Used to verify Round 5
// integration: with runRemediate=true, the assumed Decision lands in
// the AssimilationResult and on disk; with runRemediate=false, only
// the inferred-spec output lands.
func mockAssimilationLLMWithGaps() *agent.MockLLM {
	return agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"languages":["go"],"frameworks":[],"structure":"single-binary"}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-go","title":"Go backend","status":"inferred","confidence":0.95}],"entities":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[],"strategies":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"gaps":[{"category":"missing_test_framework","severity":"high","description":"no tests configured"}]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-testing","title":"Use go test","status":"assumed","rationale":"fills missing_test_framework gap"}]}`}},
	)
}

func TestAssimilateRunsRemediationByDefault(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLMWithGaps()

	result, err := RunAssimilate(context.Background(), llm, fs, true, nil)
	require.NoError(t, err)

	// Result includes both the inferred Decision (d-go) and the
	// remediator's assumed Decision (d-testing).
	var ids []string
	for _, d := range result.Decisions {
		ids = append(ids, d.ID)
	}
	assert.Contains(t, ids, "d-go", "inferred decision retained")
	assert.Contains(t, ids, "d-testing", "remediator decision merged into result")

	// And persisted to disk.
	data, err := fs.ReadFile(".borg/spec/decisions/d-testing.json")
	require.NoError(t, err)
	var got spec.Decision
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, spec.DecisionStatusAssumed, got.Status)
}

func TestAssimilateNoRemediateSkipsRemediation(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLMWithGaps()

	result, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	// Gap is reported in the result but not acted on.
	require.Len(t, result.Gaps, 1)
	assert.Equal(t, "missing_test_framework", result.Gaps[0].Category)

	// Remediator's d-testing Decision is NOT in the result.
	for _, d := range result.Decisions {
		assert.NotEqual(t, "d-testing", d.ID, "remediator output must not land when --no-remediate")
	}

	// And NOT on disk.
	_, err = fs.ReadFile(".borg/spec/decisions/d-testing.json")
	require.Error(t, err, "no file should be written for the unremediated gap")
}

func TestAssimilateRemediationSkippedWhenNoGaps(t *testing.T) {
	fs := setupAssimilateFS(t)
	// Standard mock — gap_analyst returns no gaps, so even with
	// runRemediate=true the remediator LLM call is skipped.
	llm := mockAssimilationLLM()

	result, err := RunAssimilate(context.Background(), llm, fs, true, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Gaps)

	// Mock had a 6th scripted response (intended for the remediator),
	// but it should not have been consumed because gaps were empty.
	// CallCount is 5 (scout + 3 analyzers + gap_analyst).
	assert.Equal(t, 5, llm.CallCount(), "no LLM call for remediator when gaps are empty")
}

// TestAssimilateRecordsRemediationHistoryEvent confirms that one
// `remediation_run` history event is written per pass, with summary
// counts in the rationale. This closes the Round 5 followup
// (gap-closeout.md ambiguity 3).
func TestAssimilateRecordsRemediationHistoryEvent(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLMWithGaps()

	_, err := RunAssimilate(context.Background(), llm, fs, true, nil)
	require.NoError(t, err)

	files, err := fs.ListDir(".borg/history")
	require.NoError(t, err)

	var found []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".json") {
			continue
		}
		data, err := fs.ReadFile(f)
		require.NoError(t, err)
		var evt struct {
			Kind      string `json:"kind"`
			Rationale string `json:"rationale"`
		}
		require.NoError(t, json.Unmarshal(data, &evt))
		if evt.Kind == "remediation_run" {
			found = append(found, evt.Rationale)
		}
	}
	require.Len(t, found, 1, "exactly one remediation_run event per pass")
	assert.Contains(t, found[0], "1 gap")
	assert.Contains(t, found[0], "1 decisions")
}

// TestAssimilateNoHistoryEventWithoutRemediation confirms the event is
// only emitted on an actual remediation pass — no event when
// --no-remediate is set, no event when there are no gaps.
func TestAssimilateNoHistoryEventWithoutRemediation(t *testing.T) {
	fs := setupAssimilateFS(t)
	llm := mockAssimilationLLMWithGaps()

	_, err := RunAssimilate(context.Background(), llm, fs, false, nil)
	require.NoError(t, err)

	files, _ := fs.ListDir(".borg/history")
	for _, f := range files {
		data, err := fs.ReadFile(f)
		require.NoError(t, err)
		assert.NotContains(t, string(data), `"kind": "remediation_run"`, "no event when --no-remediate")
	}
}
