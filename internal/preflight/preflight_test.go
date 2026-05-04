package preflight_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/preflight"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPreflightFixture prepares a one-Approach workstream and the
// surrounding spec graph. The parent Feature has one existing Decision
// that the pre-flight agent can point at for spec-resolved questions.
func setupPreflightFixture(t *testing.T) (
	specio.FS, *spec.SpecGraph, *state.FileStateStore, spec.Workstream, map[string]spec.Approach,
) {
	t.Helper()
	fs := specio.NewMemFS()
	for _, d := range []string{".borg/spec/features", ".borg/spec/decisions", ".borg/spec/strategies", ".borg/spec/approaches", ".borg/history", ".borg/state"} {
		require.NoError(t, fs.MkdirAll(d, 0o755))
	}

	dec := spec.Decision{
		ID: "dec-bcrypt", Title: "Use bcrypt", Status: spec.DecisionStatusActive,
		Confidence: 0.95, Rationale: "Industry standard; cost factor 12.",
	}
	feat := spec.Feature{
		ID: "feat-auth", Title: "Authentication", Status: spec.FeatureStatusActive,
		Description: "We hash passwords with bcrypt.",
		Decisions:   []string{"dec-bcrypt"},
		Approaches:  []string{"app-oauth"},
	}
	app := spec.Approach{
		ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth",
		Body:          "Implement OAuth login with bcrypt-hashed passwords.",
		ArtifactPaths: []string{"internal/auth/oauth.go"},
		Decisions:     []string{"dec-bcrypt"},
		CreatedAt:     time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, feat.Description))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-bcrypt", dec, dec.Rationale))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", app, app.Body))

	g := spec.BuildGraph(
		[]spec.Feature{feat}, nil,
		[]spec.Decision{dec},
		nil,
		[]spec.Approach{app},
		spec.TraceabilityIndex{},
	)
	store := state.NewFileStateStore(fs, ".borg/state")

	ws := spec.Workstream{
		ID: "ws-auth",
		Steps: []spec.PlanStep{
			{ID: "step-oauth-1", Order: 1, ApproachID: "app-oauth", Description: "Wire OAuth provider client"},
			{ID: "step-oauth-2", Order: 2, ApproachID: "app-oauth", Description: "Add login handler"},
		},
	}
	approachesByID := map[string]spec.Approach{"app-oauth": app}

	return fs, g, store, ws, approachesByID
}

// specResolutionJSON returns an agent response that resolves all questions
// from the spec graph (no assumptions).
func specResolutionJSON(q, answer, specNode string) agent.MockResponse {
	return agent.MockResponse{Response: &agent.GenerateResponse{Content: `{
  "resolutions": [
    {"question": "` + q + `", "source": "spec", "spec_node_id": "` + specNode + `", "answer": "` + answer + `"}
  ]
}`}}
}

// assumedResolutionJSON returns an agent response that creates one assumed
// Decision with the given title/rationale/confidence.
func assumedResolutionJSON(q, answer, title, rationale string, confidence float64) agent.MockResponse {
	payload := `{
  "resolutions": [
    {"question": "` + q + `", "source": "assumed", "answer": "` + answer + `", "assumed_decision": {"title": "` + title + `", "rationale": "` + rationale + `", "confidence": ` + floatJSON(confidence) + `}}
  ]
}`
	return agent.MockResponse{Response: &agent.GenerateResponse{Content: payload}}
}

func floatJSON(f float64) string {
	return strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
}
func formatFloat(f float64) string { return fmt.Sprintf("%.2f", f) }

// emptyResolutionsJSON — the happy path exit: agent finds nothing to
// clarify.
func emptyResolutionsJSON() agent.MockResponse {
	return agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"resolutions": []}`}}
}

// "fmt" import only needed for test formatting helpers above.
// (keeping the import minimal)

func TestPreflightNoAmbiguitiesExitsEarly(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)
	llm := agent.NewMockLLM(emptyResolutionsJSON())

	report, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 3)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, 1, report.Rounds, "a single empty-resolutions round is enough")
	assert.Empty(t, report.Resolutions)
	assert.Empty(t, report.AssumedDecisions)
	assert.Empty(t, report.DriftedApproaches)
	assert.Equal(t, 1, llm.CallCount(), "only one agent call when no ambiguities found")
}

func TestPreflightSpecResolutionSkipsDecisionCreation(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)
	llm := agent.NewMockLLM(
		specResolutionJSON(
			"What hashing algorithm should the password flow use?",
			"bcrypt with cost factor 12",
			"dec-bcrypt",
		),
		emptyResolutionsJSON(), // round 2 — agent now finds nothing
	)

	report, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 3)
	require.NoError(t, err)

	require.Len(t, report.Resolutions, 1)
	assert.Equal(t, preflight.SourceSpec, report.Resolutions[0].Source)
	assert.Equal(t, "dec-bcrypt", report.Resolutions[0].SpecNodeID)
	assert.Empty(t, report.AssumedDecisions, "spec resolution must not create a new Decision")
	assert.Empty(t, report.DriftedApproaches)

	// Approach body should now include the rendered resolution.
	bodyData, err := fs.ReadFile(".borg/spec/approaches/app-oauth.md")
	require.NoError(t, err)
	assert.Contains(t, string(bodyData), "Pre-flight Resolutions")
	assert.Contains(t, string(bodyData), "spec: dec-bcrypt")
}

func TestPreflightAssumedResolutionCreatesDecisionAndCascades(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)

	// Seed the Approach's state entry to `live` so cascade's drift marking
	// actually has something to flip.
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID: "app-oauth", SpecHash: "sha256:prior", Status: state.StatusLive,
	}))

	llm := agent.NewMockLLM(
		// Round 1: one assumed resolution.
		assumedResolutionJSON(
			"What session TTL should we use when the user ticks 'remember me'?",
			"30 days, rotating refresh tokens daily.",
			"Session TTL remember-me",
			"No business requirement exists; picking 30 days as a common default.",
			0.6,
		),
		// Cascade invocation for the new Decision — rewriter returns no change.
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"revised_body": "We hash passwords with bcrypt.", "changed": false, "rationale": "New decision does not affect parent prose"}`}},
		// Round 2: agent finds nothing new → early exit.
		emptyResolutionsJSON(),
	)

	report, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 3)
	require.NoError(t, err)

	require.Len(t, report.Resolutions, 1)
	assert.Equal(t, preflight.SourceAssumed, report.Resolutions[0].Source)
	assert.NotEmpty(t, report.Resolutions[0].DecisionID)

	require.Len(t, report.AssumedDecisions, 1)
	newDec := report.AssumedDecisions[0]
	assert.Equal(t, spec.DecisionStatusAssumed, newDec.Status)
	assert.Equal(t, 0.6, newDec.Confidence)
	assert.True(t, strings.HasPrefix(newDec.ID, "session-ttl-remember-me"),
		"ID should be slugged from the title, got %q", newDec.ID)

	// Decision file should have landed.
	_, err = fs.ReadFile(".borg/spec/decisions/" + newDec.ID + ".json")
	assert.NoError(t, err)
}

func TestPreflightRejectsInvalidConfidence(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)

	// Confidence >= 1.0 must be rejected — DJ-071 requires the range (0,1).
	llm := agent.NewMockLLM(
		assumedResolutionJSON("Q?", "A.", "Some title", "Some rationale", 1.0),
	)

	_, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 3)
	assert.Error(t, err)
}

func TestPreflightRejectsSpecResolutionWithoutSpecNode(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)

	malformed := agent.MockResponse{Response: &agent.GenerateResponse{Content: `{
  "resolutions": [
    {"question": "Q?", "source": "spec", "answer": "A."}
  ]
}`}}
	llm := agent.NewMockLLM(malformed)

	_, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 3)
	assert.Error(t, err)
}

func TestPreflightExitsWhenAgentStabilises(t *testing.T) {
	fs, g, store, ws, approaches := setupPreflightFixture(t)

	// Two rounds of questions then one empty round. Preflight should stop
	// at the empty round, not burn the full maxRounds budget.
	llm := agent.NewMockLLM(
		specResolutionJSON("first?", "answer one", "dec-bcrypt"),
		specResolutionJSON("second?", "answer two", "dec-bcrypt"),
		emptyResolutionsJSON(),
	)

	report, err := preflight.Preflight(context.Background(), llm, fs, g, store, ws, approaches, 5)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Rounds, "stopped on the empty-resolutions round")
	assert.Len(t, report.Resolutions, 2)
}
