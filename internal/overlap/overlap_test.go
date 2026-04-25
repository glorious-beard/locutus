package overlap_test

import (
	"testing"

	"github.com/chetan/locutus/internal/overlap"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// approachesWith returns a lookup map for the given approaches keyed by ID.
func approachesWith(as ...spec.Approach) map[string]spec.Approach {
	out := make(map[string]spec.Approach, len(as))
	for _, a := range as {
		out[a.ID] = a
	}
	return out
}

func TestDetectEmptyPlanReturnsNil(t *testing.T) {
	reports := overlap.Detect(nil, nil)
	assert.Nil(t, reports)
}

func TestDetectSingleWorkstreamReturnsNil(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-1", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
		},
	}
	approaches := approachesWith(spec.Approach{ID: "app-a", ArtifactPaths: []string{"a.go"}})
	reports := overlap.Detect(plan, approaches)
	assert.Nil(t, reports)
}

func TestDetectParallelWorkstreamsSharingFile(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
			{ID: "ws-b", Steps: []spec.PlanStep{{ApproachID: "app-b"}}},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"internal/auth/auth.go", "internal/auth/auth_test.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"internal/auth/auth.go", "internal/api/handler.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	require := assert.New(t)
	require.Len(reports, 1)
	require.Equal("ws-a", reports[0].WorkstreamA)
	require.Equal("ws-b", reports[0].WorkstreamB)
	require.Equal([]string{"internal/auth/auth.go"}, reports[0].SharedFiles)
}

func TestDetectIgnoresSequentialWorkstreams(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
			{
				ID:        "ws-b",
				DependsOn: []spec.WorkstreamDependency{{WorkstreamID: "ws-a"}},
				Steps:     []spec.PlanStep{{ApproachID: "app-b"}},
			},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"shared.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"shared.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	assert.Empty(t, reports, "B depends on A → sequential → shared file is fine")
}

func TestDetectIgnoresTransitivelySequentialWorkstreams(t *testing.T) {
	// A → B → C (transitive). A and C share a file but C waits for B which waits
	// for A; sequential by transitive closure.
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
			{
				ID:        "ws-b",
				DependsOn: []spec.WorkstreamDependency{{WorkstreamID: "ws-a"}},
				Steps:     []spec.PlanStep{{ApproachID: "app-b"}},
			},
			{
				ID:        "ws-c",
				DependsOn: []spec.WorkstreamDependency{{WorkstreamID: "ws-b"}},
				Steps:     []spec.PlanStep{{ApproachID: "app-c"}},
			},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"transitive.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"other.go"}},
		spec.Approach{ID: "app-c", ArtifactPaths: []string{"transitive.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	assert.Empty(t, reports, "transitive A → B → C means A and C are sequential")
}

func TestDetectIntraWorkstreamSharingAllowed(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{
				ID: "ws-a",
				Steps: []spec.PlanStep{
					{ApproachID: "app-a"},
					{ApproachID: "app-b"},
				},
			},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"shared.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"shared.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	assert.Empty(t, reports, "two Approaches in same workstream sharing files is fine")
}

func TestDetectUsesUnionOfArtifactPathsAndExpectedFiles(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{
				ID: "ws-a",
				Steps: []spec.PlanStep{
					{ApproachID: "app-a", ExpectedFiles: []string{"step-only.go"}},
				},
			},
			{
				ID: "ws-b",
				Steps: []spec.PlanStep{
					{ApproachID: "app-b"},
				},
			},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"approach-only.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"step-only.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	require := assert.New(t)
	require.Len(reports, 1, "step's ExpectedFiles must be in the overlap input set")
	require.Equal([]string{"step-only.go"}, reports[0].SharedFiles)
}

func TestDetectMultipleSharedFilesSorted(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
			{ID: "ws-b", Steps: []spec.PlanStep{{ApproachID: "app-b"}}},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"z.go", "a.go", "m.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"m.go", "z.go", "a.go", "extra.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	require := assert.New(t)
	require.Len(reports, 1)
	require.Equal([]string{"a.go", "m.go", "z.go"}, reports[0].SharedFiles)
}

func TestDetectDeterministicPairOrder(t *testing.T) {
	plan := &spec.MasterPlan{
		Workstreams: []spec.Workstream{
			{ID: "ws-c", Steps: []spec.PlanStep{{ApproachID: "app-c"}}},
			{ID: "ws-a", Steps: []spec.PlanStep{{ApproachID: "app-a"}}},
			{ID: "ws-b", Steps: []spec.PlanStep{{ApproachID: "app-b"}}},
		},
	}
	approaches := approachesWith(
		spec.Approach{ID: "app-a", ArtifactPaths: []string{"x.go"}},
		spec.Approach{ID: "app-b", ArtifactPaths: []string{"x.go"}},
		spec.Approach{ID: "app-c", ArtifactPaths: []string{"x.go"}},
	)

	reports := overlap.Detect(plan, approaches)
	require := assert.New(t)
	require.Len(reports, 3, "C(3,2)=3 pairs all sharing x.go")
	// Pairs ordered by sorted IDs: (a,b), (a,c), (b,c).
	require.Equal("ws-a", reports[0].WorkstreamA)
	require.Equal("ws-b", reports[0].WorkstreamB)
	require.Equal("ws-a", reports[1].WorkstreamA)
	require.Equal("ws-c", reports[1].WorkstreamB)
	require.Equal("ws-b", reports[2].WorkstreamA)
	require.Equal("ws-c", reports[2].WorkstreamB)
}

func TestFormatReportsHumanReadable(t *testing.T) {
	reports := []overlap.Report{
		{WorkstreamA: "ws-a", WorkstreamB: "ws-b", SharedFiles: []string{"x.go", "y.go"}},
	}
	s := overlap.FormatReports(reports)
	assert.Contains(t, s, "ws-a")
	assert.Contains(t, s, "ws-b")
	assert.Contains(t, s, "x.go")
	assert.Contains(t, s, "y.go")
}
