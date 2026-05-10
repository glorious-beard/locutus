package cmd

import (
	"context"
	"encoding/json"
	"path"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptRefinerProseCascade builds a mock response for the existing
// `refiner` agent — the prose-cascade pass that runs after supersede's
// mechanical id rewrites complete.
func scriptRefinerProseCascade(t *testing.T, revisedBody string) agent.MockResponse {
	t.Helper()
	payload := cascade.RewriteResult{
		RevisedBody: revisedBody,
		Changed:     true,
		Rationale:   "rewritten to reflect the supersede motivation",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return agent.MockResponse{
		AgentID:  "refiner",
		Response: &agent.AgentOutput{Content: string(body)},
	}
}

// fixtureSupersedeProject builds a memfs project rooted at .borg/
// with one decision, one feature referencing it, one approach with
// the decision in its Decisions[], and one bug for the feature.
// Mirrors the cascade-package fixture shape but as a runnable
// project (manifest.json present, agents discoverable from embedded
// scaffolds).
func fixtureSupersedeProject(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-target", spec.Decision{
		ID: "dec-target", Title: "Target decision",
		Status: spec.DecisionStatusProposed, Confidence: 0.9,
		Rationale: "to be superseded",
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-alpha", spec.Feature{
		ID: "feat-alpha", Title: "Alpha feature", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-target"},
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-alpha.md", spec.Approach{
		ID: "app-alpha", Title: "Alpha approach", ParentID: "feat-alpha",
		Decisions: []string{"dec-target"},
		CreatedAt: now, UpdatedAt: now,
	}, "alpha body"))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-1", spec.Bug{
		ID: "bug-1", Title: "First bug", FeatureID: "feat-alpha",
		Severity: spec.BugSeverityHigh, Status: spec.BugStatusReported,
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	return fs
}

// scriptSupersedeDecisionResponse builds a mock-LLM response for the
// refiner-supersede-decision agent. Returns the JSON body the mock
// will hand back when the agent is invoked.
func scriptSupersedeDecisionResponse(t *testing.T, newID, newTitle string) agent.MockResponse {
	t.Helper()
	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	payload := agent.RewriteDecisionResult{
		RevisedDecision: spec.Decision{
			ID: newID, Title: newTitle,
			Status: spec.DecisionStatusProposed, Confidence: 0.85,
			Rationale: "replaces dec-target with the missing alternative",
			Alternatives: []spec.Alternative{
				{Name: "Old approach", Rationale: "originally chosen", RejectedBecause: "broke down on missing alternative"},
			},
			CreatedAt: now, UpdatedAt: now,
		},
		Rationale: "supersession addresses the missing alternative",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return agent.MockResponse{Response: &agent.AgentOutput{Content: string(body)}}
}

func scriptSupersedeFeatureResponse(t *testing.T, newID, newTitle string) agent.MockResponse {
	t.Helper()
	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	payload := agent.RewriteFeatureResult{
		RevisedFeature: spec.Feature{
			ID: newID, Title: newTitle, Status: spec.FeatureStatusProposed,
			Description: "rescoped feature",
			Decisions:   []string{"dec-target"},
			CreatedAt:   now, UpdatedAt: now,
		},
		Rationale: "feature rescoped per motivation",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return agent.MockResponse{Response: &agent.AgentOutput{Content: string(body)}}
}

func TestRunRefineSupersede_Decision_NewSlug(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	mock := agent.NewMockExecutor(
		scriptSupersedeDecisionResponse(t, "dec-replacement", "Replacement decision"),
		scriptRefinerProseCascade(t, "Feature description rewritten to reference dec-replacement (WorkOS)."),
	)

	res, err := RunRefineSupersede(context.Background(), mock, fs, "dec-target", spec.KindDecision,
		"Address: missing alternative was never evaluated", "")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Supersede)

	assert.Equal(t, "dec-target", res.Supersede.OldID)
	assert.Equal(t, "dec-replacement", res.Supersede.NewID)
	assert.False(t, res.Supersede.InPlace)
	assert.NotEmpty(t, res.Supersede.EventID)
	assert.ElementsMatch(t, []string{"feat-alpha"}, res.Supersede.FeaturesRewritten)
	assert.ElementsMatch(t, []string{"app-alpha"}, res.Supersede.ApproachesInvalidated)

	// Old decision deleted, new written.
	_, err = fs.ReadFile(".borg/spec/decisions/dec-target.json")
	assert.Error(t, err, "old decision file must be deleted")
	_, err = fs.ReadFile(".borg/spec/decisions/dec-replacement.json")
	assert.NoError(t, err, "new decision file must exist")

	// Feature: id rewrite happened AND prose was refreshed by the
	// refiner cascade pass.
	feat, _, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-alpha")
	require.NoError(t, err)
	assert.Equal(t, []string{"dec-replacement"}, feat.Decisions,
		"mechanical cascade rewrote Decisions[]")
	assert.Contains(t, feat.Description, "dec-replacement",
		"prose cascade refreshed Description to reflect the new decision; left-stale prose is exactly the bug supersede must fix end-to-end")

	// Approach invalidated + Decisions[] rewritten.
	app, _, err := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-alpha.md")
	require.NoError(t, err)
	assert.True(t, app.IsInvalidated())
	assert.Equal(t, []string{"dec-replacement"}, app.Decisions)
	assert.Equal(t, res.Supersede.EventID, app.InvalidatedByEventID,
		"approach must point back at the supersede event")

	// History event persisted.
	historyDir, err := fs.ListDir(".borg/history")
	require.NoError(t, err)
	require.NotEmpty(t, historyDir, "supersede must produce one history event")
	// 2 LLM calls: 1 supersede + 1 refiner prose cascade for the
	// affected feature. Strategy/bug counts here are zero in this
	// fixture so no further calls.
	assert.Equal(t, 2, mock.CallCount(),
		"supersede + 1 prose cascade call for 1 affected feature")
}

func TestRunRefineSupersede_Decision_InPlace(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	// Same id ⇒ in-place.
	mock := agent.NewMockExecutor(scriptSupersedeDecisionResponse(t, "dec-target", "Target decision (revised)"))

	res, err := RunRefineSupersede(context.Background(), mock, fs, "dec-target", spec.KindDecision,
		"clarify alternatives", "")
	require.NoError(t, err)
	require.NotNil(t, res.Supersede)

	assert.True(t, res.Supersede.InPlace)
	assert.Equal(t, "dec-target", res.Supersede.OldID)
	assert.Equal(t, "dec-target", res.Supersede.NewID)
	assert.Empty(t, res.Supersede.FeaturesRewritten,
		"in-place: feature.Decisions[] entries unchanged")
	assert.ElementsMatch(t, []string{"app-alpha"}, res.Supersede.ApproachesInvalidated,
		"in-place still invalidates approaches")

	// Decision file rewritten in place.
	dec, _, err := specio.LoadPair[spec.Decision](fs, ".borg/spec/decisions/dec-target")
	require.NoError(t, err)
	assert.Equal(t, "Target decision (revised)", dec.Title)
}

func TestRunRefineSupersede_Feature_NewSlug(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	mock := agent.NewMockExecutor(
		scriptSupersedeFeatureResponse(t, "feat-replacement", "Replacement feature"),
		scriptRefinerProseCascade(t, "Bug description rewritten to reference feat-replacement."),
	)

	res, err := RunRefineSupersede(context.Background(), mock, fs, "feat-alpha", spec.KindFeature,
		"rescope to align with new strategy", "")
	require.NoError(t, err)
	require.NotNil(t, res.Supersede)

	assert.Equal(t, "feat-replacement", res.Supersede.NewID)
	assert.ElementsMatch(t, []string{"bug-1"}, res.Supersede.BugsRewritten)
	assert.ElementsMatch(t, []string{"app-alpha"}, res.Supersede.ApproachesInvalidated)

	// Bug FeatureID rewritten + prose refreshed.
	b, _, err := specio.LoadPair[spec.Bug](fs, ".borg/spec/bugs/bug-1")
	require.NoError(t, err)
	assert.Equal(t, "feat-replacement", b.FeatureID)
	assert.Contains(t, b.Description, "feat-replacement",
		"prose cascade refreshed bug description to reflect the superseded feature")

	// Approach ParentID rewritten.
	app, _, err := specio.LoadMarkdown[spec.Approach](fs, path.Join(".borg/spec/approaches", "app-alpha.md"))
	require.NoError(t, err)
	assert.Equal(t, "feat-replacement", app.ParentID)
	assert.True(t, app.IsInvalidated())

	assert.Equal(t, 2, mock.CallCount(),
		"supersede + 1 prose cascade call for 1 affected bug")
}

func TestRunRefineSupersede_BugRejected(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	mock := agent.NewMockExecutor()

	_, err := RunRefineSupersede(context.Background(), mock, fs, "bug-1", spec.KindBug,
		"reframe", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bug")
	assert.Equal(t, 0, mock.CallCount(),
		"bug rejection must fire before any LLM call")
}

func TestRunRefineSupersede_EmptyMotivationRejected(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	mock := agent.NewMockExecutor()

	_, err := RunRefineSupersede(context.Background(), mock, fs, "dec-target", spec.KindDecision,
		"   ", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "motivation")
	assert.Equal(t, 0, mock.CallCount())
}

func TestRunRefineSupersede_AgentEmitsWrongPrefix(t *testing.T) {
	fs := fixtureSupersedeProject(t)
	// Agent emits an id missing the dec- prefix; orchestrator must
	// reject before applying anything.
	mock := agent.NewMockExecutor(scriptSupersedeDecisionResponse(t, "totally-wrong", "x"))

	_, err := RunRefineSupersede(context.Background(), mock, fs, "dec-target", spec.KindDecision,
		"motivation", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefix")

	// Old decision still on disk — no partial write.
	_, err = fs.ReadFile(".borg/spec/decisions/dec-target.json")
	assert.NoError(t, err, "rejected agent output must not have written anything")
}

func TestRefineCmdValidate_SupersedeAndBriefMutuallyExclusive(t *testing.T) {
	c := &RefineCmd{ID: "dec-target", Brief: "x", Supersede: "y"}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestRefineCmdValidate_RollbackBlocksSupersede(t *testing.T) {
	c := &RefineCmd{ID: "dec-target", Rollback: true, Supersede: "y"}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rollback")
}
