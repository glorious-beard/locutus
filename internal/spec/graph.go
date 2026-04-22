package spec

import (
	"fmt"

	dgraph "github.com/dominikbraun/graph"
)

// RootID is the well-known ID for the GOALS.md root node.
const RootID = "GOALS.md"

// GraphNode represents a vertex in the spec dependency graph.
type GraphNode struct {
	ID   string
	Kind NodeKind
	Name string
}

// SpecGraph holds the spec dependency graph.
// Edge direction: root → (Feature | Bug | Strategy) → Decision/Approach
type SpecGraph struct {
	dag        dgraph.Graph[string, GraphNode]
	features   map[string]*Feature
	bugs       map[string]*Bug
	decisions  map[string]*Decision
	strategies map[string]*Strategy
	approaches map[string]*Approach
}

// Feature returns the Feature for the given ID, or nil.
func (g *SpecGraph) Feature(id string) *Feature { return g.features[id] }

// Bug returns the Bug for the given ID, or nil.
func (g *SpecGraph) Bug(id string) *Bug { return g.bugs[id] }

// Decision returns the Decision for the given ID, or nil.
func (g *SpecGraph) Decision(id string) *Decision { return g.decisions[id] }

// Strategy returns the Strategy for the given ID, or nil.
func (g *SpecGraph) Strategy(id string) *Strategy { return g.strategies[id] }

// Approach returns the Approach for the given ID, or nil.
func (g *SpecGraph) Approach(id string) *Approach { return g.approaches[id] }

// Nodes returns a map of all graph nodes, keyed by ID.
func (g *SpecGraph) Nodes() map[string]GraphNode {
	adj, _ := g.dag.AdjacencyMap()
	nodes := make(map[string]GraphNode, len(adj))
	for id := range adj {
		n, _ := g.dag.Vertex(id)
		nodes[id] = n
	}
	return nodes
}

// EdgeCount returns the number of edges in the graph.
func (g *SpecGraph) EdgeCount() int {
	edges, _ := g.dag.Edges()
	return len(edges)
}

// BlastRadius describes the downstream impact of changing a spec node.
type BlastRadius struct {
	Root       GraphNode
	Features   []GraphNode
	Bugs       []GraphNode
	Decisions  []GraphNode
	Strategies []GraphNode
	Approaches []GraphNode
}

// BuildGraph constructs a SpecGraph from spec data.
// Edge direction (parent → children):
//
//	root → Feature, root → Bug, root → Strategy
//	Feature → Decision (via Feature.Decisions)
//	Feature → Approach (via Feature.Approaches)
//	Strategy → Decision (via Strategy.Decisions)
//	Strategy → Approach (via Strategy.Approaches)
//	Bug → Feature (via Bug.FeatureID)
func BuildGraph(
	features []Feature,
	bugs []Bug,
	decisions []Decision,
	strategies []Strategy,
	approaches []Approach,
	traces TraceabilityIndex,
) *SpecGraph {
	g := &SpecGraph{
		dag:        dgraph.New(func(n GraphNode) string { return n.ID }, dgraph.Directed()),
		features:   make(map[string]*Feature),
		bugs:       make(map[string]*Bug),
		decisions:  make(map[string]*Decision),
		strategies: make(map[string]*Strategy),
		approaches: make(map[string]*Approach),
	}

	add := func(id string, kind NodeKind, name string) {
		_ = g.dag.AddVertex(GraphNode{ID: id, Kind: kind, Name: name})
	}

	edge := func(from, to string) {
		_ = g.dag.AddEdge(from, to)
	}

	// Register root.
	add(RootID, KindGoals, "GOALS.md")

	// Register all node types.
	for i := range features {
		f := &features[i]
		add(f.ID, KindFeature, f.Title)
		g.features[f.ID] = f
	}
	for i := range bugs {
		b := &bugs[i]
		add(b.ID, KindBug, b.Title)
		g.bugs[b.ID] = b
	}
	for i := range decisions {
		d := &decisions[i]
		add(d.ID, KindDecision, d.Title)
		g.decisions[d.ID] = d
	}
	for i := range strategies {
		s := &strategies[i]
		add(s.ID, KindStrategy, s.Title)
		g.strategies[s.ID] = s
	}
	for i := range approaches {
		a := &approaches[i]
		add(a.ID, KindApproach, a.Title)
		g.approaches[a.ID] = a
	}

	// root → Feature, root → Bug, root → Strategy.
	for _, f := range features {
		edge(RootID, f.ID)
	}
	for _, b := range bugs {
		edge(RootID, b.ID)
	}
	for _, s := range strategies {
		edge(RootID, s.ID)
	}

	// Feature → Decision and Feature → Approach.
	for _, f := range features {
		for _, decID := range f.Decisions {
			edge(f.ID, decID)
		}
		for _, appID := range f.Approaches {
			edge(f.ID, appID)
		}
	}

	// Bug → Feature.
	for _, b := range bugs {
		if b.FeatureID != "" {
			edge(b.ID, b.FeatureID)
		}
	}

	// Strategy → Decision and Strategy → Approach.
	for _, s := range strategies {
		for _, decID := range s.Decisions {
			edge(s.ID, decID)
		}
		for _, appID := range s.Approaches {
			edge(s.ID, appID)
		}
	}

	return g
}

// TransitiveDeps returns every node reachable from any seed ID by forward
// traversal, filtered by the predicate, sorted in topological order so that
// dependencies precede the seeds that pulled them in. Per DJ-068, the
// planner uses this to expand a set of drifted Approaches into the full
// non-`live` reachable subgraph when assembling a workstream.
//
// Unknown seed IDs are silently skipped (the caller's set can mix IDs that
// exist in the graph with ones that don't, e.g. during a partial import).
// A nil predicate means "keep everything."
func (g *SpecGraph) TransitiveDeps(seeds []string, predicate func(GraphNode) bool) ([]GraphNode, error) {
	// Union the forward walks from every seed, deduping by ID.
	reachable := make(map[string]GraphNode)
	for _, seed := range seeds {
		for _, n := range g.ForwardWalk(seed) {
			if _, ok := reachable[n.ID]; ok {
				continue
			}
			if predicate != nil && !predicate(n) {
				continue
			}
			reachable[n.ID] = n
		}
	}
	if len(reachable) == 0 {
		return nil, nil
	}

	// Topologically sort the whole graph, then keep only members of the
	// reachable set in that order. This is simpler than extracting a
	// subgraph and handles cycles uniformly (TopologicalSort surfaces the
	// cycle error up to the caller).
	order, err := dgraph.TopologicalSort(g.dag)
	if err != nil {
		return nil, fmt.Errorf("transitive deps topological sort: %w", err)
	}
	out := make([]GraphNode, 0, len(reachable))
	for _, id := range order {
		if n, ok := reachable[id]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// ApproachesUnder returns every Approach reachable from startID by forward
// traversal — i.e. every Approach in the subtree of the given spec node.
// Used by `adopt` to scope reconciliation to a node and its descendants.
// An empty startID or RootID returns every Approach in the graph.
func (g *SpecGraph) ApproachesUnder(startID string) []Approach {
	seed := startID
	if seed == "" {
		seed = RootID
	}
	nodes := g.ForwardWalk(seed)
	if len(nodes) == 0 {
		return nil
	}
	var out []Approach
	for _, n := range nodes {
		if n.Kind != KindApproach {
			continue
		}
		if a := g.Approach(n.ID); a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// ForwardWalk returns all nodes reachable from the given ID (including the node
// itself) by following edges forward via BFS. Returns nil if id is not found.
func (g *SpecGraph) ForwardWalk(startID string) []GraphNode {
	if _, err := g.dag.Vertex(startID); err != nil {
		return nil
	}

	var result []GraphNode
	_ = dgraph.BFS(g.dag, startID, func(id string) bool {
		n, _ := g.dag.Vertex(id)
		result = append(result, n)
		return false
	})

	return result
}

// BlastRadius computes the downstream impact of the given node, categorized by kind.
func (g *SpecGraph) BlastRadius(id string) *BlastRadius {
	nodes := g.ForwardWalk(id)
	if len(nodes) == 0 {
		return &BlastRadius{}
	}

	root, _ := g.dag.Vertex(id)
	br := &BlastRadius{Root: root}

	for _, n := range nodes {
		if n.ID == id {
			continue
		}
		switch n.Kind {
		case KindFeature:
			br.Features = append(br.Features, n)
		case KindBug:
			br.Bugs = append(br.Bugs, n)
		case KindDecision:
			br.Decisions = append(br.Decisions, n)
		case KindStrategy:
			br.Strategies = append(br.Strategies, n)
		case KindApproach:
			br.Approaches = append(br.Approaches, n)
		}
	}

	return br
}

// ComputeBlastRadius computes the blast radius for the given ID.
// Returns an error if the graph is nil or the ID is not found.
func ComputeBlastRadius(g *SpecGraph, id string) (*BlastRadius, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if _, err := g.dag.Vertex(id); err != nil {
		return nil, fmt.Errorf("node %q not found in graph", id)
	}
	return g.BlastRadius(id), nil
}

// DetectCycles returns any cycles found in the graph.
func (g *SpecGraph) DetectCycles() [][]string {
	_, err := dgraph.TopologicalSort(g.dag)
	if err == nil {
		return nil
	}

	adj, _ := g.dag.AdjacencyMap()

	const (
		white = 0
		gray  = 1
		black = 2
	)

	color := make(map[string]int)
	parent := make(map[string]string)
	var cycles [][]string

	var dfs func(u string)
	dfs = func(u string) {
		color[u] = gray
		for v := range adj[u] {
			switch color[v] {
			case gray:
				cycle := []string{v}
				curr := u
				for curr != v {
					cycle = append(cycle, curr)
					curr = parent[curr]
				}
				cycles = append(cycles, cycle)
			case white:
				parent[v] = u
				dfs(v)
			}
		}
		color[u] = black
	}

	for id := range adj {
		if color[id] == white {
			dfs(id)
		}
	}

	return cycles
}

// NewTestGraph creates a SpecGraph from explicit nodes and edges, for testing
// scenarios like cycle detection that can't be expressed via BuildGraph.
func NewTestGraph(nodes []GraphNode, edges [][2]string) *SpecGraph {
	g := &SpecGraph{
		dag:        dgraph.New(func(n GraphNode) string { return n.ID }, dgraph.Directed()),
		features:   make(map[string]*Feature),
		bugs:       make(map[string]*Bug),
		decisions:  make(map[string]*Decision),
		strategies: make(map[string]*Strategy),
		approaches: make(map[string]*Approach),
	}

	for _, n := range nodes {
		_ = g.dag.AddVertex(n)
	}
	for _, e := range edges {
		_ = g.dag.AddEdge(e[0], e[1])
	}
	return g
}
