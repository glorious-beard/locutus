package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImportFeature verifies that ImportFeature parses markdown with YAML
// frontmatter, creates a Feature with correct fields, sets status to "proposed",
// and writes the .json and .md pair to .borg/spec/features/.
func TestImportFeature(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)

	input := []byte(`---
id: feat-auth
title: User Authentication
type: feature
---

Users should be able to log in with email and password.
`)

	feat, err := ImportFeature(fs, input, "")
	assert.NoError(t, err)
	if !assert.NotNil(t, feat) {
		return
	}

	// Verify returned Feature fields.
	assert.Equal(t, "feat-auth", feat.ID)
	assert.Equal(t, "User Authentication", feat.Title)
	assert.Equal(t, spec.FeatureStatusProposed, feat.Status, "imported features should default to 'proposed'")

	// Verify files were written to the MemFS.
	jsonData, err := fs.ReadFile(".borg/spec/features/feat-auth.json")
	assert.NoError(t, err, ".json file should exist in .borg/spec/features/")
	assert.NotEmpty(t, jsonData)

	mdData, err := fs.ReadFile(".borg/spec/features/feat-auth.md")
	assert.NoError(t, err, ".md file should exist in .borg/spec/features/")
	assert.NotEmpty(t, mdData)
}

// TestImportBug verifies that ImportBug parses markdown with YAML frontmatter
// containing type=bug, severity, and feature_id, creates a Bug with correct
// fields, and sets status to "reported".
func TestImportBug(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/bugs", 0o755)

	input := []byte(`---
id: bug-login-crash
title: Login crashes on empty password
type: bug
severity: high
feature_id: feat-auth
---

When a user submits the login form with an empty password field, the
application crashes with a nil pointer dereference.
`)

	bug, err := ImportBug(fs, input, "")
	assert.NoError(t, err)
	if !assert.NotNil(t, bug) {
		return
	}

	// Verify returned Bug fields.
	assert.Equal(t, "bug-login-crash", bug.ID)
	assert.Equal(t, "Login crashes on empty password", bug.Title)
	assert.Equal(t, spec.BugStatusReported, bug.Status, "imported bugs should default to 'reported'")
	assert.Equal(t, spec.BugSeverityHigh, bug.Severity)
	assert.Equal(t, "feat-auth", bug.FeatureID)

	// Verify files were written to the MemFS.
	jsonData, err := fs.ReadFile(".borg/spec/bugs/bug-login-crash.json")
	assert.NoError(t, err, ".json file should exist in .borg/spec/bugs/")
	assert.NotEmpty(t, jsonData)

	mdData, err := fs.ReadFile(".borg/spec/bugs/bug-login-crash.md")
	assert.NoError(t, err, ".md file should exist in .borg/spec/bugs/")
	assert.NotEmpty(t, mdData)
}

// TestImportNoFrontmatterNoSourcePath verifies that ImportFeature returns an
// error when both YAML frontmatter and a source path are missing — there's
// no way to derive an id in that case.
func TestImportNoFrontmatterNoSourcePath(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg/spec/features", 0o755)

	input := []byte(`This is just plain markdown without any frontmatter.

It should not be importable as a feature.
`)

	feat, err := ImportFeature(fs, input, "")
	assert.Error(t, err, "should return error when both frontmatter and source path are absent")
	assert.Nil(t, feat)
}

// TestImportFeatureDerivesFromPath verifies that when frontmatter is missing
// or partial, ImportFeature derives id from the filename and title from the
// first markdown heading.
func TestImportFeatureDerivesFromPath(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`# Dashboard

Some prose describing the dashboard feature.
`)

	feat, err := ImportFeature(fs, input, "docs/dashboard.md")
	require.NoError(t, err)
	require.NotNil(t, feat)

	assert.Equal(t, "feat-dashboard", feat.ID)
	assert.Equal(t, "Dashboard", feat.Title)
	assert.Equal(t, spec.FeatureStatusProposed, feat.Status)

	_, err = fs.ReadFile(".borg/spec/features/feat-dashboard.json")
	assert.NoError(t, err)
}

// TestImportFeatureFilenameAlreadyPrefixed checks that a filename that
// already begins with the feat- prefix isn't doubled.
func TestImportFeatureFilenameAlreadyPrefixed(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	feat, err := ImportFeature(fs, []byte(`# Onboarding`), "specs/feat-onboarding.md")
	require.NoError(t, err)
	assert.Equal(t, "feat-onboarding", feat.ID)
}

// TestImportFeatureTitleFallsBackToFilename verifies humanizeBaseName is
// used when the body has no heading.
func TestImportFeatureTitleFallsBackToFilename(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	feat, err := ImportFeature(fs, []byte(`No heading here, just prose.`), "docs/user-onboarding.md")
	require.NoError(t, err)
	assert.Equal(t, "feat-user-onboarding", feat.ID)
	assert.Equal(t, "User Onboarding", feat.Title)
}

// TestRunImportSkipTriageWritesFeature checks the Phase B admission path with
// triage bypassed: no LLM call, feature is written to the spec dir.
func TestRunImportSkipTriageWritesFeature(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`---
id: feat-admit
title: Direct Admit
type: feature
---
Body.
`)

	result, err := RunImport(context.Background(), nil, fs, input, "", "feature", true, true, false, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	assert.Equal(t, "feat-admit", result.FeatureID)
	assert.Equal(t, ".borg/spec/features/feat-admit", result.Destination)

	// Verify the pair actually landed.
	_, err = fs.ReadFile(".borg/spec/features/feat-admit.json")
	assert.NoError(t, err)
}

// TestRunImportDryRunSkipsWrite confirms --dry-run previews without touching
// the spec dir.
func TestRunImportDryRunSkipsWrite(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`---
id: feat-dry
title: Dry-run
---
Body.
`)

	result, err := RunImport(context.Background(), nil, fs, input, "", "feature", true, true, true, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	assert.True(t, result.DryRun)
	assert.Equal(t, "feat-dry", result.FeatureID)

	// No file should have been written.
	_, err = fs.ReadFile(".borg/spec/features/feat-dry.json")
	assert.Error(t, err)
}

// TestRunImportNoPlanSkipsGeneration confirms that --no-plan stops the
// flow after admission: the feature is written but the planning pass
// (spec generation) does not fire, and the result reports SkippedPlan.
func TestRunImportNoPlanSkipsGeneration(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`---
id: feat-noplan
title: No Plan
---
Body.
`)

	// skipTriage=true so we don't reach for an LLM. noPlan=true so even
	// if we did, the planning pass would short-circuit.
	result, err := RunImport(context.Background(), nil, fs, input, "", "feature", true, true, false, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	assert.Equal(t, "feat-noplan", result.FeatureID)
	assert.True(t, result.SkippedPlan, "result.SkippedPlan should be true when --no-plan is set")
	assert.Nil(t, result.Generated, "no GenerationSummary should be present when planning is skipped")

	// Feature landed; no decisions/strategies/approaches were generated.
	_, err = fs.ReadFile(".borg/spec/features/feat-noplan.json")
	assert.NoError(t, err)
	subdirs, _ := fs.ListDir(".borg/spec/decisions")
	assert.Empty(t, subdirs, "no decisions should be generated when planning is skipped")
}

// TestRunImportSkipTriageNoLLM confirms that --skip-triage admits without
// any LLM call, deriving id from frontmatter when present.
func TestRunImportSkipTriageNoLLM(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`---
id: feat-direct
title: Direct Admit
---
Body.
`)

	result, err := RunImport(context.Background(), nil, fs, input, "", "feature", true, true, false, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	assert.Equal(t, "feat-direct", result.FeatureID)
	assert.Nil(t, result.Verdict, "no LLM should have been called with skip_triage=true")
}

// TestRunImportWorkflowAcceptsViaIntake exercises the workflow-driven
// path (Phase 9): skipTriage=false fires the workflow's intake step,
// which drives the LLM call and stashes IntakeResult. The cmd-layer
// reads the verdict, persists the feature, and reports admission.
//
// noPlan=true keeps the planning pass off so the test doesn't need to
// script the full spec-generation council.
func TestRunImportWorkflowAcceptsViaIntake(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`# Realtime Dashboard

A live view of project health.
`)

	intakeJSON := `{"id":"feat-realtime-dashboard","title":"Realtime dashboard","accepted":true,"reason":"aligns with goals"}`
	mock := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	result, err := RunImport(context.Background(), mock, fs, input, "docs/dashboard.md", "feature", false, true, false, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	require.NotNil(t, result.Verdict, "workflow intake should populate Verdict")
	assert.Equal(t, "feat-realtime-dashboard", result.Verdict.ID)
	assert.True(t, result.Verdict.Accepted)
	assert.Equal(t, "feat-realtime-dashboard", result.FeatureID)

	// Feature landed via the post-workflow persistence path.
	_, err = fs.ReadFile(".borg/spec/features/feat-realtime-dashboard.json")
	assert.NoError(t, err, "workflow path should persist the feature after admission")

	// Exactly one LLM call (intake); no planning pass with noPlan=true.
	assert.Equal(t, 1, mock.CallCount(), "noPlan=true should keep planning off")
}

// TestRunImportWorkflowRejectsViaIntake confirms an LLM rejection
// short-circuits before persistence — same behaviour the legacy
// resolveImportMetadata path produces.
func TestRunImportWorkflowRejectsViaIntake(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`# Mobile App

Build a native iOS/Android app.
`)

	intakeJSON := `{"id":"feat-mobile-app","title":"Mobile app","accepted":false,"reason":"out of scope: CLI only"}`
	mock := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	result, err := RunImport(context.Background(), mock, fs, input, "docs/mobile.md", "feature", false, true, false, nil)
	require.NoError(t, err)
	assert.False(t, result.Accepted, "intake rejection should propagate to result.Accepted")
	require.NotNil(t, result.Verdict)
	assert.False(t, result.Verdict.Accepted)
	assert.Equal(t, "out of scope: CLI only", result.Verdict.Reason)

	// No file should have been written.
	_, err = fs.ReadFile(".borg/spec/features/feat-mobile-app.json")
	assert.Error(t, err, "rejected admission should not persist a feature")
}

// TestRunImportThreadsImportedContentToScout locks the DJ-124 Phase 6
// contract from the cmd-layer side: when `locutus import` runs the
// post-admission planning pass, the admitted document body is threaded
// through SpecGenRequest.Imported and reaches the scout's user message
// via projectScout. End-to-end via RunImport with a fully scripted
// workflow, asserting the scout receives an `## Imported content`
// section containing the document body.
func TestRunImportThreadsImportedContentToScout(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fs, "test-project"))
	require.NoError(t, fs.WriteFile("GOALS.md", []byte("# Goals\nBuild collaboration tooling.\n"), 0o644))
	// Drop the convergence agent so the workflow executor doesn't burn an
	// extra LLM call after the council exits — mirrors TestRefineGoals*.
	require.NoError(t, fs.Remove(".borg/agents/convergence.md"))

	importedBody := "Admins need a live dashboard showing project health updates as work progresses."
	input := []byte("# Real-time dashboard\n\n" + importedBody + "\n")

	intakeJSON := `{"id":"feat-realtime-dashboard","title":"Realtime dashboard","accepted":true,"reason":"aligns with goals"}`
	scout0 := `{
		"domain_read":"team-collaboration tooling",
		"technology_options":["transport: WebSocket vs SSE"],
		"implicit_assumptions":["scale: 1k concurrent admins"],
		"watch_outs":[],
		"axes_open":[{
			"id":"live-update-transport",
			"description":"How do dashboard tiles receive live updates?",
			"source_evidence":["Imported PRD requests real-time updates."],
			"surfaced_by":["feat-realtime-dashboard"]
		}],
		"new_nodes":[
			{"kind":"feature","id":"feat-realtime-dashboard","title":"Real-time dashboard","summary":"Admins see live updates.","decisions":[]}
		],
		"converged":false
	}`
	decisionJSON := `{
		"id":"dec-websocket-transport",
		"title":"Adopt WebSocket transport",
		"rationale":"Bidirectional; low-latency; widely supported.",
		"architect_rationale":"Imported PRD requires real-time updates.",
		"confidence":0.9,
		"alternatives":[{
			"name":"Server-Sent Events",
			"rationale":"Simpler unidirectional fit",
			"rejected_because":"no client-to-server channel",
			"citations":[{"kind":"web","reference":"https://html.spec.whatwg.org/sse","excerpt":"one-way only"}]
		}],
		"citations":[{"kind":"imported","reference":"dashboard","excerpt":"real-time updates"}],
		"axes":["live-update-transport"],
		"surfaced_by":["feat-realtime-dashboard"]
	}`
	featureJSON := `{
		"id":"feat-realtime-dashboard",
		"title":"Real-time dashboard",
		"description":"Admins see live updates as events arrive over the WebSocket channel.",
		"decisions":["dec-websocket-transport"]
	}`
	scoutConverged := `{
		"domain_read":"team-collaboration tooling",
		"technology_options":[],
		"implicit_assumptions":[],
		"watch_outs":[],
		"axes_open":[],
		"new_nodes":[],
		"converged":true
	}`

	mock := agent.NewMockExecutor(
		// Intake call (skipTriage=false drives this first).
		agent.MockResponse{Response: &agent.AgentOutput{Content: intakeJSON, Model: "test-model"}},
		// Planning pass — full DJ-124 council scripted.
		agent.MockResponse{AgentID: "spec_scout", Response: &agent.AgentOutput{Content: scout0, Model: "m"}},
		agent.MockResponse{AgentID: "spec_decision_elaborator", Response: &agent.AgentOutput{Content: decisionJSON, Model: "m"}},
		agent.MockResponse{AgentID: "spec_feature_elaborator", Response: &agent.AgentOutput{Content: featureJSON, Model: "m"}},
		agent.MockResponse{AgentID: "spec_reconciler", Response: &agent.AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "architect_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "devops_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "sre_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "cost_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "spec_scout", Response: &agent.AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	// skipTriage=false (run intake), noPlan=false (run planning pass).
	result, err := RunImport(context.Background(), mock, fs, input, "docs/dashboard.md", "feature", false, false, false, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	require.NotNil(t, result.Generated, "planning pass should fire and report a GenerationSummary")
	assert.Equal(t, "feat-realtime-dashboard", result.FeatureID)

	// The scout must have received the imported document body in an
	// `## Imported content` section — proof that runFeatureGeneration
	// threaded the admitted document through SpecGenRequest.Imported and
	// the unified workflow projected it onto the scout's user message.
	var scoutSawImport bool
	for _, c := range mock.Calls() {
		if c.Def.ID != "spec_scout" {
			continue
		}
		for _, m := range c.Input.Messages {
			if strings.Contains(m.Content, "## Imported content") && strings.Contains(m.Content, importedBody) {
				scoutSawImport = true
				break
			}
		}
		if scoutSawImport {
			break
		}
	}
	assert.True(t, scoutSawImport,
		"the scout's user message must include the imported document body — DJ-124 Phase 6 wires SpecGenRequest.Imported from cmd through to projectScout")
}

// TestRunImportWorkflowDryRunNoSideEffects verifies the workflow-
// driven path honours --dry-run: the intake LLM call still fires (so
// the operator sees the verdict in the preview) but no on-disk side
// effects occur.
func TestRunImportWorkflowDryRunNoSideEffects(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))

	input := []byte(`# Drypreview

A preview of the dry-run path.
`)

	intakeJSON := `{"id":"feat-drypreview","title":"Drypreview","accepted":true}`
	mock := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	result, err := RunImport(context.Background(), mock, fs, input, "docs/drypreview.md", "feature", false, true, true, nil)
	require.NoError(t, err)
	require.True(t, result.Accepted)
	assert.True(t, result.DryRun)
	assert.Equal(t, "feat-drypreview", result.FeatureID)

	// Dry-run preserves the original on-disk state.
	_, err = fs.ReadFile(".borg/spec/features/feat-drypreview.json")
	assert.Error(t, err, "dry-run should not persist a feature even after admission")
	_, err = fs.ReadFile(".borg/spec/features/feat-drypreview.md")
	assert.Error(t, err, "dry-run should not persist the .md sidecar either")

	// Exactly one LLM call (intake) — planning is off (noPlan=true).
	assert.Equal(t, 1, mock.CallCount())
}
