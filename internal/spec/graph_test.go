package spec_test

import (
	"sort"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

func buildTestGraph() *spec.SpecGraph {
	features := []spec.Feature{
		{ID: "feat-auth", Title: "Authentication", Decisions: []string{"dec-backend-lang", "dec-auth-method"}},
		{ID: "feat-payments", Title: "Payments", Decisions: []string{"dec-payment-provider"}},
	}
	bugs := []spec.Bug{
		{ID: "bug-login-crash", Title: "Login crash", FeatureID: "feat-auth"},
	}
	decisions := []spec.Decision{
		{ID: "dec-backend-lang", Title: "Backend Language", Feature: "feat-auth"},
		{ID: "dec-auth-method", Title: "Auth Method", Feature: "feat-auth"},
		{ID: "dec-payment-provider", Title: "Payment Provider", Feature: "feat-payments"},
	}
	strategies := []spec.Strategy{
		{ID: "strat-go", Title: "Use Go", DecisionID: "dec-backend-lang"},
		{ID: "strat-jwt", Title: "Use JWT", DecisionID: "dec-auth-method"},
		{ID: "strat-stripe", Title: "Use Stripe", DecisionID: "dec-payment-provider"},
	}
	traces := spec.TraceabilityIndex{
		Entries: map[string]spec.TraceEntry{
			"cmd/main.go":                 {StrategyID: "strat-go"},
			"internal/auth/handler.go":    {StrategyID: "strat-go"},
			"internal/auth/jwt.go":        {StrategyID: "strat-jwt"},
			"internal/auth/middleware.go":  {StrategyID: "strat-jwt"},
			"internal/payments/stripe.go": {StrategyID: "strat-stripe"},
		},
	}
	return spec.BuildGraph(features, bugs, decisions, strategies, traces)
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
	assert.Len(t, nodes, 15, "1 root + 2 features + 1 bug + 3 decisions + 3 strategies + 5 files")

	expectedKinds := map[string]spec.NodeKind{
		spec.RootID:                   spec.KindGoals,
		"feat-auth":                   spec.KindFeature,
		"feat-payments":               spec.KindFeature,
		"bug-login-crash":             spec.KindBug,
		"dec-backend-lang":            spec.KindDecision,
		"dec-auth-method":             spec.KindDecision,
		"dec-payment-provider":        spec.KindDecision,
		"strat-go":                    spec.KindStrategy,
		"strat-jwt":                   spec.KindStrategy,
		"strat-stripe":                spec.KindStrategy,
		"cmd/main.go":                 spec.KindFile,
		"internal/auth/handler.go":    spec.KindFile,
		"internal/auth/jwt.go":        spec.KindFile,
		"internal/auth/middleware.go":  spec.KindFile,
		"internal/payments/stripe.go": spec.KindFile,
	}
	for id, kind := range expectedKinds {
		node, ok := nodes[id]
		assert.True(t, ok, "expected node %q", id)
		if ok {
			assert.Equal(t, kind, node.Kind, "node %q kind", id)
		}
	}

	assert.Equal(t, 15, g.EdgeCount())
}

func TestTypedAccessors(t *testing.T) {
	g := buildTestGraph()

	f := g.Feature("feat-auth")
	assert.NotNil(t, f)
	assert.Equal(t, "Authentication", f.Title)
	assert.Equal(t, []string{"dec-backend-lang", "dec-auth-method"}, f.Decisions)

	s := g.Strategy("strat-go")
	assert.NotNil(t, s)
	assert.Equal(t, "dec-backend-lang", s.DecisionID)

	d := g.Decision("dec-backend-lang")
	assert.NotNil(t, d)
	assert.Equal(t, "Backend Language", d.Title)

	b := g.Bug("bug-login-crash")
	assert.NotNil(t, b)
	assert.Equal(t, "feat-auth", b.FeatureID)

	// Non-existent IDs return nil.
	assert.Nil(t, g.Feature("nope"))
	assert.Nil(t, g.Decision("nope"))
}

func TestForwardWalkFromRoot(t *testing.T) {
	g := buildTestGraph()
	nodes := g.ForwardWalk(spec.RootID)
	assert.Len(t, nodes, 15, "root reaches all nodes")
}

func TestForwardWalkFromFeature(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("feat-auth"))
	assert.Equal(t, []string{
		"cmd/main.go", "dec-auth-method", "dec-backend-lang", "feat-auth",
		"internal/auth/handler.go", "internal/auth/jwt.go", "internal/auth/middleware.go",
		"strat-go", "strat-jwt",
	}, ids)
}

func TestForwardWalkFromDecision(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("dec-backend-lang"))
	assert.Equal(t, []string{"cmd/main.go", "dec-backend-lang", "internal/auth/handler.go", "strat-go"}, ids)
}

func TestForwardWalkFromStrategy(t *testing.T) {
	g := buildTestGraph()
	ids := graphNodeIDs(g.ForwardWalk("strat-jwt"))
	assert.Equal(t, []string{"internal/auth/jwt.go", "internal/auth/middleware.go", "strat-jwt"}, ids)
}

func TestBlastRadiusFromRoot(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius(spec.RootID)

	assert.Equal(t, spec.RootID, br.Root.ID)
	assert.Equal(t, spec.KindGoals, br.Root.Kind)
	assert.Len(t, br.Features, 2)
	assert.Len(t, br.Bugs, 1)
	assert.Len(t, br.Decisions, 3)
	assert.Len(t, br.Strategies, 3)
	assert.Len(t, br.Files, 5)
}

func TestBlastRadiusFromFeature(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("feat-auth")

	assert.Equal(t, "feat-auth", br.Root.ID)
	assert.Equal(t, spec.KindFeature, br.Root.Kind)
	assert.Empty(t, br.Features)
	assert.Empty(t, br.Bugs)
	assert.Equal(t, []string{"dec-auth-method", "dec-backend-lang"}, graphNodeIDs(br.Decisions))
	assert.Equal(t, []string{"strat-go", "strat-jwt"}, graphNodeIDs(br.Strategies))
	assert.Equal(t, []string{"cmd/main.go", "internal/auth/handler.go", "internal/auth/jwt.go", "internal/auth/middleware.go"}, graphNodeIDs(br.Files))
}

func TestBlastRadiusFromDecision(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("dec-auth-method")

	assert.Equal(t, spec.KindDecision, br.Root.Kind)
	assert.Empty(t, br.Decisions)
	assert.Equal(t, []string{"strat-jwt"}, graphNodeIDs(br.Strategies))
	assert.Equal(t, []string{"internal/auth/jwt.go", "internal/auth/middleware.go"}, graphNodeIDs(br.Files))
}

func TestBlastRadiusFromStrategy(t *testing.T) {
	g := buildTestGraph()
	br := g.BlastRadius("strat-stripe")

	assert.Equal(t, spec.KindStrategy, br.Root.Kind)
	assert.Empty(t, br.Decisions)
	assert.Empty(t, br.Strategies)
	assert.Equal(t, []string{"internal/payments/stripe.go"}, graphNodeIDs(br.Files))
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
	assert.Equal(t, []string{
		"bug-login-crash", "cmd/main.go", "dec-auth-method", "dec-backend-lang",
		"feat-auth", "internal/auth/handler.go", "internal/auth/jwt.go",
		"internal/auth/middleware.go", "strat-go", "strat-jwt",
	}, ids)
}

func TestForwardWalkUnknownID(t *testing.T) {
	g := buildTestGraph()
	assert.Empty(t, g.ForwardWalk("nonexistent-id"))
}
