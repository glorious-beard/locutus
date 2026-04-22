package cmd

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/workstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusEmptySpec verifies that GatherStatus returns zero counts and
// GoalsPresent=false when the spec directory contains only the minimal init
// files (manifest.json and traces.json) with no GOALS.md.
func TestStatusEmptySpec(t *testing.T) {
	fs := specio.NewMemFS()

	// Minimal init structure: .borg/manifest.json and .borg/spec/traces.json
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)

	manifest := spec.Manifest{ProjectName: "test-project", Version: "0.1.0"}
	manifestData, err := json.Marshal(manifest)
	assert.NoError(t, err)
	assert.NoError(t, fs.WriteFile(".borg/manifest.json", manifestData, 0o644))

	traces := spec.TraceabilityIndex{Entries: map[string]spec.TraceEntry{}}
	tracesData, err := json.Marshal(traces)
	assert.NoError(t, err)
	assert.NoError(t, fs.WriteFile(".borg/spec/traces.json", tracesData, 0o644))

	// No GOALS.md exists.

	sd := GatherStatus(fs)

	assert.Equal(t, render.StatusData{
		GoalsPresent:  false,
		FeatureCount:  0,
		DecisionCount: 0,
		StrategyCount: 0,
		BugCount:      0,
	}, sd)
}

// TestStatusPopulatedSpec verifies that GatherStatus correctly counts features,
// decisions, strategies, and detects GOALS.md when the spec is populated.
func TestStatusPopulatedSpec(t *testing.T) {
	fs := specio.NewMemFS()

	// Set up directory structure.
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)
	fs.MkdirAll(".borg/spec/decisions", 0o755)
	fs.MkdirAll(".borg/spec/strategies", 0o755)

	// Write manifest.
	manifest := spec.Manifest{ProjectName: "test-project", Version: "0.1.0"}
	manifestData, err := json.Marshal(manifest)
	assert.NoError(t, err)
	assert.NoError(t, fs.WriteFile(".borg/manifest.json", manifestData, 0o644))

	// Write traces.
	traces := spec.TraceabilityIndex{Entries: map[string]spec.TraceEntry{}}
	tracesData, err := json.Marshal(traces)
	assert.NoError(t, err)
	assert.NoError(t, fs.WriteFile(".borg/spec/traces.json", tracesData, 0o644))

	// Write GOALS.md.
	assert.NoError(t, fs.WriteFile(".borg/GOALS.md", []byte("# Goals\n\nShip v1.\n"), 0o644))

	// Save 2 features via SavePair.
	feat1 := spec.Feature{ID: "feat-auth", Title: "Authentication", Status: spec.FeatureStatusProposed}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat1, "Users can log in.\n"))

	feat2 := spec.Feature{ID: "feat-payments", Title: "Payments", Status: spec.FeatureStatusActive}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-payments", feat2, "Process payments.\n"))

	// Save 1 decision via SavePair.
	dec1 := spec.Decision{ID: "dec-db", Title: "Use PostgreSQL", Status: spec.DecisionStatusActive, Confidence: 0.9}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-db", dec1, "PostgreSQL for persistence.\n"))

	// Save 1 strategy via SavePair.
	strat1 := spec.Strategy{ID: "strat-orm", Title: "Use GORM", Kind: spec.StrategyKindFoundational, Decisions: []string{"dec-db"}, Status: "active"}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-orm", strat1, "GORM as ORM layer.\n"))

	sd := GatherStatus(fs)

	assert.True(t, sd.GoalsPresent, "GoalsPresent should be true when GOALS.md exists")
	assert.Equal(t, 2, sd.FeatureCount, "should count 2 features")
	assert.Equal(t, 1, sd.DecisionCount, "should count 1 decision")
	assert.Equal(t, 1, sd.StrategyCount, "should count 1 strategy")
	assert.Equal(t, 0, sd.BugCount, "should count 0 bugs when none exist")
}

func TestGatherInFlightEmptyStore(t *testing.T) {
	fs := specio.NewMemFS()
	report, err := GatherInFlight(fs)
	require.NoError(t, err)
	assert.Empty(t, report.Plans)
	assert.Equal(t, ".locutus/workstreams", report.BaseDir)
}

// TestGatherInFlightPopulated writes a plan+workstream record and verifies
// the discovery report correctly surfaces step progress, next-step routing,
// and session-id / preflight metadata.
func TestGatherInFlightPopulated(t *testing.T) {
	fs := specio.NewMemFS()
	store := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-demo")

	require.NoError(t, store.SavePlan(spec.MasterPlan{
		ID:     "plan-demo",
		Prompt: "Bring OAuth to life",
	}))

	require.NoError(t, store.Save(workstream.ActiveWorkstream{
		WorkstreamID:   "ws-auth",
		PlanID:         "plan-demo",
		AgentSessionID: "claude-session-42",
		PreFlightDone:  true,
		ApproachIDs:    []string{"app-oauth"},
		Plan: spec.Workstream{
			ID:      "ws-auth",
			AgentID: "claude-code",
			Steps: []spec.PlanStep{
				{ID: "step-1", Order: 1, ApproachID: "app-oauth"},
				{ID: "step-2", Order: 2, ApproachID: "app-oauth"},
				{ID: "step-3", Order: 3, ApproachID: "app-oauth"},
			},
		},
		StepStatus: []workstream.StepProgress{
			{StepID: "step-1", Status: workstream.StepComplete},
			{StepID: "step-2", Status: workstream.StepFailed},
		},
	}))

	report, err := GatherInFlight(fs)
	require.NoError(t, err)
	require.Len(t, report.Plans, 1)

	p := report.Plans[0]
	assert.Equal(t, "plan-demo", p.PlanID)
	assert.Equal(t, "Bring OAuth to life", p.PlanPrompt)
	require.Len(t, p.Workstreams, 1)

	ws := p.Workstreams[0]
	assert.Equal(t, "ws-auth", ws.WorkstreamID)
	assert.Equal(t, "claude-code", ws.AgentID)
	assert.Equal(t, "claude-session-42", ws.AgentSessionID)
	assert.True(t, ws.PreFlightDone)
	assert.Equal(t, []string{"app-oauth"}, ws.ApproachIDs)
	assert.Equal(t, 3, ws.StepTotal)
	assert.Equal(t, 1, ws.StepsComplete)
	assert.Equal(t, 1, ws.StepsFailed)
	assert.Equal(t, 1, ws.StepsPending)
	assert.Equal(t, "step-3", ws.NextStepID, "NextStepID must be the first non-complete step in plan order")
}

func TestGatherInFlightSkipsSubdirsWithoutPlanYAML(t *testing.T) {
	fs := specio.NewMemFS()
	// Create an orphan subdir with no plan.yaml — must not appear.
	require.NoError(t, fs.MkdirAll(".locutus/workstreams/orphan", 0o755))

	report, err := GatherInFlight(fs)
	require.NoError(t, err)
	assert.Empty(t, report.Plans)
}
