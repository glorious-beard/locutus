package cmd

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/agent"
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
