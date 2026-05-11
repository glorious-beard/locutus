package cmd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/reconcile"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	legacyState "github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdoptWorkflow_SynthFanoutShape locks in the per-parent
// fanout: one item per feature / strategy with no Approach child,
// sorted by id for deterministic ordering.
func TestAdoptWorkflow_SynthFanoutShape(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))

	now := time.Now()
	// Two bare features (no Approaches) — both should fan out.
	for _, id := range []string{"feat-zeta", "feat-alpha"} {
		require.NoError(t, specio.SavePair(fs, ".borg/spec/features/"+id, spec.Feature{
			ID: id, Title: "Title " + id, Status: spec.FeatureStatusActive,
			CreatedAt: now, UpdatedAt: now,
		}, "body"))
	}
	// One strategy WITH an Approach attached — should NOT fan out.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-x", spec.Strategy{
		ID: "strat-x", Title: "X", Kind: spec.StrategyKindFoundational,
		Status: "active", Approaches: []string{"app-existing"},
	}, "strategy body"))

	g := spec.BuildGraph(
		[]spec.Feature{
			{ID: "feat-zeta", Title: "Title feat-zeta", Status: spec.FeatureStatusActive},
			{ID: "feat-alpha", Title: "Title feat-alpha", Status: spec.FeatureStatusActive},
		},
		nil, nil,
		[]spec.Strategy{{ID: "strat-x", Title: "X", Kind: spec.StrategyKindFoundational, Status: "active", Approaches: []string{"app-existing"}}},
		nil, spec.TraceabilityIndex{},
	)

	state := &AdoptState{FSys: fs, Graph: g}
	items, err := fanoutAdoptSynthesize(state)
	require.NoError(t, err)
	require.Len(t, items, 2, "one fanout item per bare parent; the strategy already has an Approach")

	ids := make([]string, 0, len(items))
	for _, raw := range items {
		var it adoptSynthFanoutItem
		require.NoError(t, json.Unmarshal([]byte(raw), &it))
		assert.Equal(t, "synthesizer", it.AgentID)
		ids = append(ids, it.ID)
	}
	assert.Equal(t, []string{"feat-alpha", "feat-zeta"}, ids,
		"fanout items are sorted by parent id for deterministic ordering")
}

// TestAdoptWorkflow_SynthFanoutEmptyWhenAllHaveApproaches
// covers the no-op path: every parent already carries an Approach,
// so the fanout returns nil and the executor short-circuits without
// dispatching.
func TestAdoptWorkflow_SynthFanoutEmptyWhenAllHaveApproaches(t *testing.T) {
	g := spec.BuildGraph(
		[]spec.Feature{{ID: "feat-x", Title: "X", Status: spec.FeatureStatusActive, Approaches: []string{"app-existing"}}},
		nil, nil, nil, nil, spec.TraceabilityIndex{},
	)
	state := &AdoptState{FSys: specio.NewMemFS(), Graph: g}
	items, err := fanoutAdoptSynthesize(state)
	require.NoError(t, err)
	assert.Nil(t, items, "every parent has an Approach => zero fanout items")
}

// TestAdoptWorkflow_RegenFanoutSkipsMissingEvents covers the
// soft-degrade: invalidated Approaches whose supersede event file
// vanished from .borg/history must be filtered out of the fanout with
// a slog.Warn rather than crashing the workflow. Matches the legacy
// regenerateInvalidatedApproaches behavior (and its test).
func TestAdoptWorkflow_RegenFanoutSkipsMissingEvents(t *testing.T) {
	fs := buildRegenFixture(t,
		regenApproach{id: "app-good", eventID: "evt-present"},
		regenApproach{id: "app-bad", eventID: "evt-missing"},
	)
	loaded, err := spec.LoadSpec(fs)
	require.NoError(t, err)

	state := &AdoptState{FSys: fs, Loaded: loaded}
	items, err := fanoutAdoptRegenerate(state)
	require.NoError(t, err)
	require.Len(t, items, 1, "only approaches with a readable supersede event fan out")

	var it adoptRegenFanoutItem
	require.NoError(t, json.Unmarshal([]byte(items[0]), &it))
	assert.Equal(t, "app-good", it.ApproachID,
		"missing-event approach must be filtered out, not the present one")
	assert.Equal(t, "approach-regenerator", it.AgentID)
}

// TestAdoptWorkflow_RegenFanoutEmptyWhenNoInvalidated covers
// the no-op path for adopt runs where no prior supersede left
// invalidated Approaches behind.
func TestAdoptWorkflow_RegenFanoutEmptyWhenNoInvalidated(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	loaded, err := spec.LoadSpec(fs)
	require.NoError(t, err)

	state := &AdoptState{FSys: fs, Loaded: loaded}
	items, err := fanoutAdoptRegenerate(state)
	require.NoError(t, err)
	assert.Nil(t, items, "no invalidated approaches => empty fanout")
}

// TestAdoptWorkflow_Conditionals locks in the gating logic that
// drives the workflow's short-circuit paths. Each conditional is a
// pure function over AdoptState — exercising them directly keeps
// the workflow shape's contract pinned without standing up the full
// dispatcher / executor for every variant. The integration tests
// (adopt_test.go / adopt_integration_test.go) cover the runtime end
// of the same gates.
func TestAdoptWorkflow_Conditionals(t *testing.T) {
	// condSynthRunnable + condRegenRunnable: both gate on the
	// dispatcher (no-LLM adopt run skips synth/regen wholesale).
	// regen also needs Loaded — buildAdoptState pairs them, but the
	// conditional defends against a state that's missing one.
	assert.False(t, condSynthRunnable(nil), "nil state must be false")
	assert.False(t, condSynthRunnable(&AdoptState{}), "no dispatcher => false")
	dummy := struct{ agentDispatcherTag }{}
	dispatcher := agentDispatcherWrapper{tag: &dummy}
	assert.True(t, condSynthRunnable(&AdoptState{Dispatcher: dispatcher}), "dispatcher set => true")

	assert.False(t, condRegenRunnable(&AdoptState{Dispatcher: dispatcher}), "no Loaded => false")
	assert.True(t, condRegenRunnable(&AdoptState{Dispatcher: dispatcher, Loaded: &spec.Loaded{}}),
		"dispatcher + Loaded => true")

	// condResumeClassifyRunnable: dry-run skips the resume protocol.
	assert.False(t, condResumeClassifyRunnable(&AdoptState{Cfg: AdoptConfig{DryRun: true}}),
		"dry-run skips resume classifier")
	assert.True(t, condResumeClassifyRunnable(&AdoptState{}), "non-dry-run runs resume classifier")

	// condReconcileClassifyRunnable: the resume short-circuit pivots
	// here. PlanToResume!=nil means resume_classify already pinned a
	// plan; reconcile + prereqs + plan_candidates all skip.
	assert.True(t, condReconcileClassifyRunnable(&AdoptState{}), "no resume plan => classify runs")
	assert.False(t, condReconcileClassifyRunnable(&AdoptState{PlanToResume: &spec.MasterPlan{ID: "p"}}),
		"resume plan in hand => reconcile skips")

	// condPlanCandidatesRunnable: every gate must be open. Walk the
	// matrix one rejection at a time so a regression in any single
	// guard surfaces with a specific failure.
	planFn := func(_ context.Context, _ agent.PlanRequest) (*spec.MasterPlan, error) { return nil, nil }
	dispatchFn := func(_ context.Context, _ *spec.MasterPlan, _ string, _ map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
		return nil, nil
	}
	driftCandidate := []reconcile.Classification{{Approach: spec.Approach{ID: "app-x"}, Status: legacyState.StatusDrifted}}
	full := &AdoptState{
		Cfg:             AdoptConfig{Plan: planFn, Dispatch: dispatchFn},
		PrereqsOK:       true,
		Classifications: driftCandidate,
	}
	assert.True(t, condPlanCandidatesRunnable(full), "all gates open => true")

	dryRun := *full
	dryRun.Cfg.DryRun = true
	assert.False(t, condPlanCandidatesRunnable(&dryRun), "dry-run blocks planner")

	resuming := *full
	resuming.PlanToResume = &spec.MasterPlan{ID: "p"}
	assert.False(t, condPlanCandidatesRunnable(&resuming), "resume in hand blocks planner")

	prereqFail := *full
	prereqFail.PrereqsOK = false
	assert.False(t, condPlanCandidatesRunnable(&prereqFail), "prereq failure blocks planner")

	noPlanFn := *full
	noPlanFn.Cfg.Plan = nil
	assert.False(t, condPlanCandidatesRunnable(&noPlanFn), "no plan func => no planner")

	noDispatchFn := *full
	noDispatchFn.Cfg.Dispatch = nil
	assert.False(t, condPlanCandidatesRunnable(&noDispatchFn), "no dispatch func => no planner")

	emptyCandidates := *full
	emptyCandidates.Classifications = nil
	assert.False(t, condPlanCandidatesRunnable(&emptyCandidates), "no candidates => no planner")

	// condPlanReady: persist_plan + dispatch_and_verify gate on state.Plan.
	assert.False(t, condPlanReady(&AdoptState{}), "no plan => false")
	assert.True(t, condPlanReady(&AdoptState{Plan: &spec.MasterPlan{ID: "p"}}), "plan present => true")

	// condPreflightRunnable: preflight skips on a resumed plan (the
	// prior run already did pre-flight per DJ-073). Fresh-plan runs
	// fire normally.
	assert.False(t, condPreflightRunnable(&AdoptState{}), "no plan => skip")
	resumed := &AdoptState{Plan: &spec.MasterPlan{ID: "p"}, PlanToResume: &spec.MasterPlan{ID: "p"}}
	assert.False(t, condPreflightRunnable(resumed), "resumed plan => skip preflight")
	fresh := &AdoptState{Plan: &spec.MasterPlan{ID: "p"}}
	assert.True(t, condPreflightRunnable(fresh), "fresh plan => run preflight")
}

// agentDispatcherTag + agentDispatcherWrapper provide a no-op
// AgentDispatcher for the conditional tests — condSynthRunnable only
// cares that the field is non-nil, not what the dispatcher does.
type agentDispatcherTag struct{}

type agentDispatcherWrapper struct{ tag *struct{ agentDispatcherTag } }

func (a agentDispatcherWrapper) Dispatch(_ context.Context, _ agent.AgentDef, _ agent.AgentInput, _ agent.DispatchOptions) (*agent.AgentOutput, error) {
	return nil, nil
}

// regenApproach is a tiny fixture descriptor used by
// buildRegenFixture to keep the test source readable.
type regenApproach struct {
	id      string
	eventID string
}

// buildRegenFixture writes a project shape with one approach per
// regenApproach entry. Approaches with eventID="evt-missing" have
// their event file deliberately absent from .borg/history.
func buildRegenFixture(t *testing.T, approaches ...regenApproach) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))

	now := time.Now()
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-anchor", spec.Feature{
		ID: "feat-anchor", Title: "Anchor", Status: spec.FeatureStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	for _, a := range approaches {
		require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/"+a.id+".md", spec.Approach{
			ID:                   a.id,
			Title:                a.id,
			ParentID:             "feat-anchor",
			InvalidatedByEventID: a.eventID,
			CreatedAt:            now,
			UpdatedAt:            now,
		}, "body"))
		// Only write the event file when the test asks for "present".
		if a.eventID == "evt-present" {
			evt := history.Event{
				ID: a.eventID, Timestamp: now,
				Kind:     history.EventKindNodeSuperseded,
				TargetID: "dec-old", NewValue: "dec-new",
				Supersede: &history.SupersedeRecord{
					NodeKind:                   "decision",
					Motivation:                 "test",
					ApproachesInvalidated:      []string{a.id},
				},
			}
			body, err := json.MarshalIndent(evt, "", "  ")
			require.NoError(t, err)
			require.NoError(t, fs.WriteFile(".borg/history/"+a.eventID+".json", body, 0o644))
		}
	}
	return fs
}
