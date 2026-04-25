package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedPlanner returns a PlanFunc that emits the given plans in order.
// Tracks each request it receives so callers can inspect prompt evolution
// across retries. If exhausted, returns an error.
type scriptedPlanner struct {
	plans    []*spec.MasterPlan
	calls    int
	requests []agent.PlanRequest
}

func (s *scriptedPlanner) PlanFunc() PlanFunc {
	return func(ctx context.Context, req agent.PlanRequest) (*spec.MasterPlan, error) {
		s.requests = append(s.requests, req)
		if s.calls >= len(s.plans) {
			return nil, assertionsExhaustedErr
		}
		p := s.plans[s.calls]
		s.calls++
		return p, nil
	}
}

var assertionsExhaustedErr = stringErr("scripted planner exhausted")

type stringErr string

func (s stringErr) Error() string { return string(s) }

// twoParallelWorkstreams returns a plan with ws-a and ws-b not connected
// by depends_on. Each step references the named approach.
func twoParallelWorkstreams(approachA, approachB string) *spec.MasterPlan {
	return &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: approachA}}},
			{ID: "ws-b", Steps: []spec.PlanStep{{ApproachID: approachB}}},
		},
	}
}

// sequentialWorkstreams returns a plan where ws-b depends on ws-a.
func sequentialWorkstreams(approachA, approachB string) *spec.MasterPlan {
	return &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: approachA}}},
			{
				ID:        "ws-b",
				DependsOn: []spec.WorkstreamDependency{{WorkstreamID: "ws-a"}},
				Steps:     []spec.PlanStep{{ApproachID: approachB}},
			},
		},
	}
}

func TestPlanWithOverlapRetryNoOverlap(t *testing.T) {
	approaches := map[string]spec.Approach{
		"app-a": {ID: "app-a", ArtifactPaths: []string{"a.go"}},
		"app-b": {ID: "app-b", ArtifactPaths: []string{"b.go"}},
	}
	planner := &scriptedPlanner{plans: []*spec.MasterPlan{twoParallelWorkstreams("app-a", "app-b")}}

	plan, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{Prompt: "p"}, approaches)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, 1, planner.calls, "no retry when first plan has no overlap")
}

func TestPlanWithOverlapRetrySucceedsOnSecondCall(t *testing.T) {
	approaches := map[string]spec.Approach{
		"app-conflict-a": {ID: "app-conflict-a", ArtifactPaths: []string{"shared.go"}},
		"app-conflict-b": {ID: "app-conflict-b", ArtifactPaths: []string{"shared.go"}},
		"app-clean-a":    {ID: "app-clean-a", ArtifactPaths: []string{"a.go"}},
		"app-clean-b":    {ID: "app-clean-b", ArtifactPaths: []string{"b.go"}},
	}
	planner := &scriptedPlanner{plans: []*spec.MasterPlan{
		twoParallelWorkstreams("app-conflict-a", "app-conflict-b"),
		twoParallelWorkstreams("app-clean-a", "app-clean-b"),
	}}

	plan, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{Prompt: "p"}, approaches)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, 2, planner.calls)
}

func TestPlanWithOverlapRetrySequentialResolution(t *testing.T) {
	// Plan #2 keeps the same files but adds a depends_on edge — overlap
	// detector treats it as sequential, so retry succeeds without changing
	// the file sets.
	approaches := map[string]spec.Approach{
		"app-a": {ID: "app-a", ArtifactPaths: []string{"shared.go"}},
		"app-b": {ID: "app-b", ArtifactPaths: []string{"shared.go"}},
	}
	planner := &scriptedPlanner{plans: []*spec.MasterPlan{
		twoParallelWorkstreams("app-a", "app-b"),
		sequentialWorkstreams("app-a", "app-b"),
	}}

	plan, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{Prompt: "p"}, approaches)
	require.NoError(t, err)
	require.Len(t, plan.Workstreams[1].DependsOn, 1, "second plan added depends_on")
}

func TestPlanWithOverlapRetryErrorsAfterPersistent(t *testing.T) {
	approaches := map[string]spec.Approach{
		"app-a": {ID: "app-a", ArtifactPaths: []string{"shared.go"}},
		"app-b": {ID: "app-b", ArtifactPaths: []string{"shared.go"}},
	}
	// 4 plans (initial + 3 retries) all with the same overlap → error.
	planner := &scriptedPlanner{plans: []*spec.MasterPlan{
		twoParallelWorkstreams("app-a", "app-b"),
		twoParallelWorkstreams("app-a", "app-b"),
		twoParallelWorkstreams("app-a", "app-b"),
		twoParallelWorkstreams("app-a", "app-b"),
	}}

	_, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{Prompt: "p"}, approaches)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlapping workstreams")
	assert.Contains(t, err.Error(), "shared.go")
	assert.Equal(t, maxOverlapRetries+1, planner.calls, "initial + 3 retries")
}

func TestPlanWithOverlapRetryAugmentsPromptWithReport(t *testing.T) {
	approaches := map[string]spec.Approach{
		"app-a": {ID: "app-a", ArtifactPaths: []string{"shared-sentinel.go"}},
		"app-b": {ID: "app-b", ArtifactPaths: []string{"shared-sentinel.go"}},
		"app-x": {ID: "app-x", ArtifactPaths: []string{"x.go"}},
		"app-y": {ID: "app-y", ArtifactPaths: []string{"y.go"}},
	}
	planner := &scriptedPlanner{plans: []*spec.MasterPlan{
		twoParallelWorkstreams("app-a", "app-b"),
		twoParallelWorkstreams("app-x", "app-y"),
	}}

	_, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{Prompt: "original"}, approaches)
	require.NoError(t, err)

	require.Len(t, planner.requests, 2)
	first := planner.requests[0].Prompt
	second := planner.requests[1].Prompt

	assert.Equal(t, "original", first, "first call gets the original prompt unchanged")
	assert.Contains(t, second, "original", "retry preserves the original prompt")
	assert.Contains(t, second, "Overlap conflicts", "retry section header present")
	assert.Contains(t, second, "shared-sentinel.go", "retry mentions the conflicting file")
	assert.Contains(t, second, "ws-a", "retry mentions conflicting workstream")
	assert.Contains(t, second, "ws-b")
	assert.True(t, strings.Contains(second, "depends_on") || strings.Contains(second, "merge"),
		"retry feedback names the resolution paths")
}

func TestPlanWithOverlapRetryPropagatesPlannerError(t *testing.T) {
	planner := &scriptedPlanner{plans: nil} // exhausted on first call
	_, err := planWithOverlapRetry(context.Background(), planner.PlanFunc(), agent.PlanRequest{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scripted planner exhausted")
}
