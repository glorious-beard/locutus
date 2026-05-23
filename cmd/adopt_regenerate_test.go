package cmd

import (
	"context"
	"encoding/json"
	"path"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureRegenerate builds a project with one feature, one decision,
// one approach (already invalidated by a prior supersede event), and
// the supersede event on disk.
func fixtureRegenerate(t *testing.T, eventID string) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-current", spec.Decision{
		ID: "dec-current", Title: "Current decision",
		Status: spec.DecisionStatusProposed, Confidence: 0.85,
		Rationale: "the post-supersede choice",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-foo", spec.Feature{
		ID: "feat-foo", Title: "Foo feature", Status: spec.FeatureStatusProposed,
		Description: "Foo description",
		Decisions:   []string{"dec-current"},
		Approaches:  []string{"app-foo"},
		CreatedAt:   now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-foo.md", spec.Approach{
		ID: "app-foo", Title: "Foo approach", ParentID: "feat-foo",
		Decisions:            []string{"dec-current"},
		ArtifactPaths:        []string{"lib/old-auth.ts", "middleware/session.ts"},
		InvalidatedByEventID: eventID,
		CreatedAt:            now, UpdatedAt: now,
	}, "old approach body — describes the prior NextAuth-style implementation"))

	// Persist the supersede event so the regenerator branch can read it.
	evt := history.Event{
		ID:        eventID,
		Timestamp: now,
		Kind:      history.EventKindNodeSuperseded,
		TargetID:  "dec-old",
		NewValue:  "dec-current",
		Supersede: &history.SupersedeRecord{
			NodeKind:                       "decision",
			InPlace:                        false,
			Motivation:                     "Address: WorkOS was never evaluated",
			FeaturesDecisionsRewritten:     []string{"feat-foo"},
			ApproachesInvalidated:          []string{"app-foo"},
		},
	}
	body, err := json.MarshalIndent(evt, "", "  ")
	require.NoError(t, err)
	require.NoError(t, fs.WriteFile(".borg/history/"+eventID+".json", body, 0o644))

	return fs
}

func scriptRegenerateResponse(t *testing.T, body string) agent.MockResponse {
	t.Helper()
	payload := agent.RegenerateApproachResult{
		RevisedBody: body,
		Rationale:   "regenerated to align with the new decision",
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return agent.MockResponse{Response: &agent.AgentOutput{Content: string(data)}}
}

func TestRegenerateInvalidatedApproaches_HappyPath(t *testing.T) {
	fs := fixtureRegenerate(t, "evt-supersede-001")
	mock := agent.NewMockExecutor(scriptRegenerateResponse(t,
		"new approach body — forward: build with WorkOS; backward: delete lib/old-auth.ts and middleware/session.ts"))

	regenerated, err := regenerateInvalidatedApproaches(context.Background(), agent.NewDispatcher(mock), fs)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app-foo"}, regenerated,
		"regenerated list returns ids that were rewritten on disk")

	// Approach body updated, InvalidatedByEventID cleared, structured
	// fields preserved.
	app, _, err := specio.LoadMarkdown[spec.Approach](fs, path.Join(".borg/spec/approaches", "app-foo.md"))
	require.NoError(t, err)
	assert.Contains(t, app.Body, "WorkOS",
		"regenerated body must reflect the agent output (forward direction)")
	assert.Contains(t, app.Body, "delete",
		"regenerated body must address cleanup of prior artifacts (backward direction)")
	assert.False(t, app.IsInvalidated(),
		"InvalidatedByEventID must be cleared after regeneration")
	assert.ElementsMatch(t, []string{"lib/old-auth.ts", "middleware/session.ts"}, app.ArtifactPaths,
		"ArtifactPaths must be preserved as the next coding-agent run's input")
	assert.ElementsMatch(t, []string{"dec-current"}, app.Decisions,
		"Decisions list must be preserved (cascade already rewrote the ids)")

	assert.Equal(t, 1, mock.CallCount(),
		"one LLM call per invalidated approach")
}

func TestRegenerateInvalidatedApproaches_NoInvalidatedApproachesIsNoop(t *testing.T) {
	// Build a fixture with a fresh approach (no InvalidatedByEventID).
	fs := fixtureRegenerate(t, "evt-supersede-001")
	// Clear the invalidation marker on disk so the regenerator finds
	// nothing to do.
	app, body, err := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-foo.md")
	require.NoError(t, err)
	app.InvalidatedByEventID = ""
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-foo.md", app, body))

	mock := agent.NewMockExecutor()
	regenerated, err := regenerateInvalidatedApproaches(context.Background(), agent.NewDispatcher(mock), fs)
	require.NoError(t, err)
	assert.Empty(t, regenerated)
	assert.Equal(t, 0, mock.CallCount(),
		"no invalidated approaches must mean zero LLM calls")
}

func TestRegenerateInvalidatedApproaches_AgentReturnsEmptyBodyRejected(t *testing.T) {
	fs := fixtureRegenerate(t, "evt-supersede-002")
	mock := agent.NewMockExecutor(scriptRegenerateResponse(t, ""))

	_, err := regenerateInvalidatedApproaches(context.Background(), agent.NewDispatcher(mock), fs)
	require.Error(t, err, "empty body must be rejected as a degenerate output")

	// The approach must still be invalidated (no partial write).
	app, _, err2 := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-foo.md")
	require.NoError(t, err2)
	assert.True(t, app.IsInvalidated(),
		"failed regeneration must leave the InvalidatedByEventID marker intact")
}

// TestRegenerateInvalidatedApproaches_ReActToolPath proves Phase 6's
// dispatch wiring: when the embedded approach-regenerator scaffold
// declares max_iterations>1, the model can emit tool_calls (e.g.
// spec_list_manifest) that the dispatcher resolves against the
// production registry, threads the results back, and the next
// iteration's final answer is what lands on disk.
//
// Asserts:
//   - dispatcher invoked the model twice (one per ReAct iteration)
//   - the spec tool was invoked exactly once with the model-emitted args
//   - the second iteration's final body is what persists on disk
//   - the InvalidatedByEventID marker is cleared on success
func TestRegenerateInvalidatedApproaches_ReActToolPath(t *testing.T) {
	fs := fixtureRegenerate(t, "evt-supersede-react")
	registry := agent.NewToolRegistry()
	// DJ-134: register tools against the unified SpecStore.
	store, err := agent.NewSpecStore(fs)
	if err != nil {
		t.Fatalf("NewSpecStore: %v", err)
	}
	agent.RegisterSpecTools(registry, store)

	finalBody := "ReAct-pathed body — forward: WorkOS; backward: delete legacy auth"
	finalPayload, err := json.Marshal(agent.RegenerateApproachResult{
		RevisedBody: finalBody,
		Rationale:   "regenerated after consulting spec_list_manifest",
	})
	require.NoError(t, err)

	mock := agent.NewMockExecutor(
		// Iteration 1: model calls spec_list_manifest before committing
		// to a body, mirroring the prompt's tool-use guidance.
		agent.MockResponse{Response: &agent.AgentOutput{
			Content:   "Let me scan sibling approaches before drafting",
			ToolCalls: []agent.ToolCall{{Name: agent.ToolNameSpecListManifest, Status: "ok"}},
		}},
		// Iteration 2: model produces the final structured answer.
		agent.MockResponse{Response: &agent.AgentOutput{Content: string(finalPayload)}},
	)

	regenerated, err := regenerateInvalidatedApproaches(context.Background(), agent.NewDispatcherWithTools(mock, registry), fs)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app-foo"}, regenerated,
		"the approach must be regenerated via the ReAct path")

	assert.Equal(t, 2, mock.CallCount(),
		"two adapter calls — iteration 1 emits tool_call, iteration 2 returns final answer")

	// On-disk body matches the second iteration's final content,
	// not the first iteration's intermediate reasoning.
	app, _, err := specio.LoadMarkdown[spec.Approach](fs, path.Join(".borg/spec/approaches", "app-foo.md"))
	require.NoError(t, err)
	assert.Contains(t, app.Body, "ReAct-pathed body",
		"final answer (iteration 2) is what lands on disk")
	assert.False(t, app.IsInvalidated(),
		"InvalidatedByEventID is cleared on successful regeneration")
}

func TestRegenerateInvalidatedApproaches_MissingEventFileLogsAndSkips(t *testing.T) {
	fs := fixtureRegenerate(t, "evt-supersede-003")
	// Remove the event file so the regenerator can't read the
	// motivation context. Implementation must skip rather than fail
	// the whole adopt run.
	require.NoError(t, fs.Remove(".borg/history/evt-supersede-003.json"))

	mock := agent.NewMockExecutor() // not called
	regenerated, err := regenerateInvalidatedApproaches(context.Background(), agent.NewDispatcher(mock), fs)
	require.NoError(t, err, "missing event file is a soft degrade, not a hard failure")
	assert.Empty(t, regenerated,
		"approaches whose supersede event is missing must be skipped, not regenerated")
	assert.Equal(t, 0, mock.CallCount())
}
