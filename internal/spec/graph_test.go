package spec_test

import (
	"sort"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// buildTestGraph builds the canonical test fixture:
//
//	root → feat-auth, root → bug-login-crash, root → strat-go
//	feat-auth → dec-x, feat-auth → app-oauth
//	strat-go → dec-y, strat-go → app-go
//	bug-login-crash → feat-auth
//
// 8 nodes, 8 edges.
func buildTestGraph() *spec.SpecGraph {
	features := []spec.Feature{
		{ID: "feat-auth", Title: "Authentication", Decisions: []string{"dec-x"}, Approaches: []string{"app-oauth"}},
	}
	bugs := []spec.Bug{
		{ID: "bug-login-crash", Title: "Login crash", FeatureID: "feat-auth"},
	}
	decisions := []spec.Decision{
		{ID: "dec-x", Title: "Backend Decision"},
		{ID: "dec-y", Title: "Strategy Decision"},
	}
	strategies := []spec.Strategy{
		{ID: "strat-go", Title: "Use Go", Decisions: []string{"dec-y"}, Approaches: []string{"app-go"}},
	}
	approaches := []spec.Approach{
		{ID: "app-oauth", Title: "OAuth Implementation", ParentID: "feat-auth", ArtifactPaths: []string{"src/auth/oauth.go"}},
		{ID: "app-go", Title: "Go Scaffold", ParentID: "strat-go", ArtifactPaths: []string{"cmd/main.go"}},
	}
	traces := spec.TraceabilityIndex{Entries: map[string]spec.TraceEntry{}}
	return spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)
}

func graphNodeIDs(nodes []spec.GraphNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	sort.Strings(ids)
	return ids
}

func TestBuildGraph(t *testing.T) {
	g := buildTestGraph()

	nodes := g.Nodes()
	assert.Len(t, nodes, 8, "1 root + 1 feature + 1 strategy + 1 bug + 2 decisions + 2 approaches")

	expectedKinds := map[string]spec.NodeKind{
		spec.RootID:        spec.KindGoals,
		"feat-auth":        spec.KindFeature,
		"strat-go":         spec.KindStrategy,
		"bug-login-crash":  spec.KindBug,
		"dec-x":            spec.KindDecision,
		"dec-y":            spec.KindDecision,
		"app-oauth":        spec.KindApproach,
		"app-go":           spec.KindApproach,
	}
	for id, kind := range expectedKinds {
		node, ok := nodes[id]
		assert.True(t, ok, "expected node %q", id)
		if ok {
			assert.Equal(t, kind, node.Kind, "node %q kind", id)
		}
	}

	assert.Equal(t, 8, g.EdgeCount())
}

func TestTypedAccessors(t *testing.T) {
	g := buildTestGraph()

	f := g.Feature("feat-auth")
	assert.NotNil(t, f)
	assert.Equal(t, "Authentication", f.Title)
	assert.Equal(t, []string{"dec-x"}, f.Decisions)
	assert.Equal(t, []string{"app-oauth"}, f.Approaches)

	s := g.Strategy("strat-go")
	assert.NotNil(t, s)
	assert.Equal(t, []string{"dec-y"}, s.Decisions)
	assert.Equal(t, []string{"app-go"}, s.Approaches)

	d := g.Decision("dec-x")
	assert.NotNil(t, d)
	assert.Equal(t, "Backend Decision", d.Title)

	b := g.Bug("bug-login-crash")
	assert.NotNil(t, b)
	assert.Equal(t, "feat-auth", b.FeatureID)

	a := g.Approach("app-oauth")
	assert.NotNil(t, a)
	assert.Equal(t, "feat-auth", a.ParentID)

	// Non-existent IDs return nil.
	assert.Nil(t, g.Feature("nope"))
	assert.Nil(t, g.Decision("nope"))
	assert.Nil(t, g.Approach("nope"))
}

func TestForwardWalkFromRoot(t *testing.T) {
	g := buildTestGraph()
	nodes := g.ForwardWalk(spec.RootID)
	assert.Len(t, nodes, 8, "root reaches all nodes")
}

func TestForwardWalkFromFeature(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("feat-auth"))
	assert.Equal(t, []string{"app-oauth", "dec-x", "feat-auth"}, ids)
}

func TestForwardWalkFromDecision(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("dec-x"))
	// Decisions are leaf nodes — no outgoing edges.
	assert.Equal(t, []string{"dec-x"}, ids)
}

func TestForwardWalkFromStrategy(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("strat-go"))
	assert.Equal(t, []string{"app-go", "dec-y", "strat-go"}, ids)
}

func TestForwardWalkFromApproach(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("app-oauth"))
	// Approaches are leaf nodes — no outgoing edges.
	assert.Equal(t, []string{"app-oauth"}, ids)
}

func TestBlastRadiusFromRoot(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius(spec.RootID)

	assert.Equal(t, spec.RootID, br.Root.ID)
	assert.Equal(t, spec.KindGoals, br.Root.Kind)
	assert.Len(t, br.Features, 1)
	assert.Len(t, br.Bugs, 1)
	assert.Len(t, br.Decisions, 2)
	assert.Len(t, br.Strategies, 1)
	assert.Len(t, br.Approaches, 2)
}

func TestBlastRadiusFromFeature(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("feat-auth")

	assert.Equal(t, "feat-auth", br.Root.ID)
	assert.Equal(t, spec.KindFeature, br.Root.Kind)
	assert.Empty(t, br.Features)
	assert.Empty(t, br.Bugs)
	assert.Equal(t, []string{"dec-x"}, graphNodeIDs(br.Decisions))
	assert.Empty(t, br.Strategies)
	assert.Equal(t, []string{"app-oauth"}, graphNodeIDs(br.Approaches))
}

func TestBlastRadiusFromDecision(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("dec-x")

	assert.Equal(t, spec.KindDecision, br.Root.Kind)
	assert.Empty(t, br.Decisions)
	assert.Empty(t, br.Strategies)
	assert.Empty(t, br.Approaches)
}

func TestBlastRadiusFromStrategy(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("strat-go")

	assert.Equal(t, spec.KindStrategy, br.Root.Kind)
	assert.Empty(t, br.Strategies)
	assert.Equal(t, []string{"dec-y"}, graphNodeIDs(br.Decisions))
	assert.Equal(t, []string{"app-go"}, graphNodeIDs(br.Approaches))
}

func TestDetectCyclesNone(t *testing.T) {
	g := buildTestGraph()
	assert.Empty(t, g.DetectCycles())
}

func TestDetectCyclesFound(t *testing.T) {
	g := spec.NewTestGraph(
		[]spec.GraphNode{
			{ID: "A", Kind: spec.KindDecision, Name: "Node A"},
			{ID: "B", Kind: spec.KindDecision, Name: "Node B"},
			{ID: "C", Kind: spec.KindDecision, Name: "Node C"},
		},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}},
	)
	cycles := g.DetectCycles()
	assert.NotEmpty(t, cycles)

	seen := map[string]bool{}
	for _, cycle := range cycles {
		for _, id := range cycle {
			seen[id] = true
		}
	}
	assert.True(t, seen["A"])
	assert.True(t, seen["B"])
	assert.True(t, seen["C"])
}

func TestBugLinksToFeature(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("bug-login-crash"))
	assert.Equal(t, []string{"app-oauth", "bug-login-crash", "dec-x", "feat-auth"}, ids)
}

func TestForwardWalkUnknownID(t *testing.T) {
	g := buildTestGraph()
	assert.Empty(t, g.ForwardWalk("nonexistent-id"))
}

func TestTransitiveDepsSingleSeed(t *testing.T) {
	g := buildTestGraph()
	nodes, err := g.TransitiveDeps([]string{"feat-auth"}, nil)
	assert.NoError(t, err)
	// feat-auth reaches itself + dec-x + app-oauth. Three nodes total.
	assert.Equal(t, []string{"app-oauth", "dec-x", "feat-auth"}, graphNodeIDs(nodes))
}

func TestTransitiveDepsMultipleSeedsUnioned(t *testing.T) {
	g := buildTestGraph()
	nodes, err := g.TransitiveDeps([]string{"feat-auth", "strat-go"}, nil)
	assert.NoError(t, err)
	// feat-auth: itself + dec-x + app-oauth
	// strat-go : itself + dec-y + app-go
	assert.Equal(t, []string{"app-go", "app-oauth", "dec-x", "dec-y", "feat-auth", "strat-go"},
		graphNodeIDs(nodes))
}

func TestTransitiveDepsPredicateFilter(t *testing.T) {
	g := buildTestGraph()
	onlyApproaches := func(n spec.GraphNode) bool { return n.Kind == spec.KindApproach }
	nodes, err := g.TransitiveDeps([]string{"feat-auth", "strat-go"}, onlyApproaches)
	assert.NoError(t, err)
	assert.Equal(t, []string{"app-go", "app-oauth"}, graphNodeIDs(nodes))
}

// TestTransitiveDepsTopoOrder pins the DJ-068 invariant: dependencies come
// first, so the planner can dispatch in declaration order without having to
// sort again. feat-auth must appear before its downstream dec-x / app-oauth.
func TestTransitiveDepsTopoOrder(t *testing.T) {
	g := buildTestGraph()
	nodes, err := g.TransitiveDeps([]string{"feat-auth"}, nil)
	assert.NoError(t, err)

	posByID := map[string]int{}
	for i, n := range nodes {
		posByID[n.ID] = i
	}
	assert.Less(t, posByID["feat-auth"], posByID["dec-x"],
		"feat-auth must precede its child dec-x")
	assert.Less(t, posByID["feat-auth"], posByID["app-oauth"],
		"feat-auth must precede its child app-oauth")
}

func TestTransitiveDepsSkipsUnknownSeeds(t *testing.T) {
	g := buildTestGraph()
	// One good seed, one bad — the bad one must be silently skipped, not
	// error out the whole walk.
	nodes, err := g.TransitiveDeps([]string{"nonexistent", "feat-auth"}, nil)
	assert.NoError(t, err)
	assert.Equal(t, []string{"app-oauth", "dec-x", "feat-auth"}, graphNodeIDs(nodes))
}

func TestTransitiveDepsEmptySeeds(t *testing.T) {
	g := buildTestGraph()
	nodes, err := g.TransitiveDeps(nil, nil)
	assert.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestTransitiveDepsAllUnknownReturnsEmpty(t *testing.T) {
	g := buildTestGraph()
	nodes, err := g.TransitiveDeps([]string{"nope-1", "nope-2"}, nil)
	assert.NoError(t, err)
	assert.Empty(t, nodes)
}
