package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// connectClient connects an in-memory client to an already-built
// server, returning the client session. Each call uses a fresh
// transport pair, so the server allocates a distinct *ServerSession
// per call — that's exactly the multi-client topology the loop tools'
// session-scoping has to survive.
func connectClient(t *testing.T, ctx context.Context, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, serverTransport, nil)
	assert.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	assert.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

// newLoopServer builds a server with a real registry sourced from an
// in-memory .borg/agents.yaml so cap-bound tests can set a low
// max_iterations. A nil yaml falls through to embedded defaults.
func newLoopServer(t *testing.T, agentsYAML string) (*mcp.Server, *agent.SpecStore) {
	t.Helper()
	fsys := specio.NewMemFS()
	if agentsYAML != "" {
		assert.NoError(t, fsys.MkdirAll(".borg", 0o755))
		assert.NoError(t, fsys.WriteFile(".borg/agents.yaml", []byte(agentsYAML), 0o644))
	}
	store, err := agent.NewSpecStore(fsys)
	assert.NoError(t, err)
	reg, err := activity.NewRegistry(fsys)
	assert.NoError(t, err)
	return NewSpecServer(store, fsys, reg, nil), store
}

func decodeLoopBegin(t *testing.T, res *mcp.CallToolResult) loopBeginOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)
	var out loopBeginOutput
	assert.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func decodeLoopStatus(t *testing.T, res *mcp.CallToolResult) loopStatusOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)
	var out loopStatusOutput
	assert.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func decodeAdvance(t *testing.T, res *mcp.CallToolResult) advanceIterationOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)
	var out advanceIterationOutput
	assert.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestSpecServer_RegistersLoopTools(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.ListTools(context.Background(), nil)
	assert.NoError(t, err)

	names := toolNames(res.Tools)
	assert.Contains(t, names, "spec_loop_begin")
	assert.Contains(t, names, "spec_loop_status")
	assert.Contains(t, names, "spec_advance_iteration")
}

func TestSpecLoopBegin_ReturnsDefaultCap(t *testing.T) {
	// nil reg → fall back to activity.DefaultMaxIterations.
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_begin",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	out := decodeLoopBegin(t, res)
	assert.Equal(t, 0, out.Iteration)
	assert.Equal(t, activity.DefaultMaxIterations, out.MaxIterations)
}

func TestSpecLoopLifecycle_BeginAdvanceConverge(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestServer(t, nil)
	args := map[string]any{"activity": "spec_refinement", "target": "goals"}

	begin, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_begin", Arguments: args})
	assert.NoError(t, err)
	assert.Equal(t, 0, decodeLoopBegin(t, begin).Iteration)

	// First advance: not converged → continue, iteration becomes 1.
	adv1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "spec_advance_iteration",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": false, "reason": "axes remain"},
	})
	assert.NoError(t, err)
	out1 := decodeAdvance(t, adv1)
	assert.True(t, out1.Continue, "non-converged advance under cap should continue")
	assert.Equal(t, 1, out1.Iteration)
	assert.Equal(t, "axes remain", out1.Reason)

	// Status mid-flight reflects iteration 1.
	st, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_status", Arguments: args})
	assert.NoError(t, err)
	stOut := decodeLoopStatus(t, st)
	assert.Equal(t, 1, stOut.Iteration)
	assert.False(t, stOut.Converged)
	assert.Equal(t, "axes remain", stOut.LastVerdict)

	// Second advance: converged → stop.
	adv2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "spec_advance_iteration",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": true, "reason": "fixed point"},
	})
	assert.NoError(t, err)
	out2 := decodeAdvance(t, adv2)
	assert.False(t, out2.Continue, "converged advance should stop the loop")
	assert.Equal(t, 2, out2.Iteration)
}

func TestSpecLoopAdvance_StopsAtCap(t *testing.T) {
	ctx := context.Background()
	// Real registry with a low cap for spec_refinement so the cap stop
	// is reachable without 20 iterations.
	server, _ := newLoopServer(t, "activities:\n  spec_refinement:\n    runtimes: [claude-code]\n    max_iterations: 2\n")
	session := connectClient(t, ctx, server)
	args := map[string]any{"activity": "spec_refinement", "target": "goals"}

	begin, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_begin", Arguments: args})
	assert.NoError(t, err)
	assert.Equal(t, 2, decodeLoopBegin(t, begin).MaxIterations, "cap should come from the registry override")

	// Advance 1 (iteration 1 < cap 2) → continue.
	adv1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "spec_advance_iteration",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": false, "reason": "still working"},
	})
	assert.NoError(t, err)
	assert.True(t, decodeAdvance(t, adv1).Continue)

	// Advance 2 (iteration 2 == cap 2) → stop even though not converged.
	adv2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "spec_advance_iteration",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": false, "reason": "still working"},
	})
	assert.NoError(t, err)
	out2 := decodeAdvance(t, adv2)
	assert.False(t, out2.Continue, "advance hitting the cap should stop the loop even when not converged")
	assert.Equal(t, 2, out2.Iteration)
}

func TestSpecLoop_SessionIsolation(t *testing.T) {
	ctx := context.Background()
	// One server, two distinct client connections → two distinct
	// *ServerSession on the server side. Running the same
	// (activity, target) on each must keep iteration counters
	// independent.
	server, _ := newLoopServer(t, "")
	sessionA := connectClient(t, ctx, server)
	sessionB := connectClient(t, ctx, server)

	args := map[string]any{"activity": "spec_refinement", "target": "goals"}

	_, err := sessionA.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_begin", Arguments: args})
	assert.NoError(t, err)
	_, err = sessionB.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_begin", Arguments: args})
	assert.NoError(t, err)

	// Advance A twice, B once.
	for i := 0; i < 2; i++ {
		_, err = sessionA.CallTool(ctx, &mcp.CallToolParams{
			Name:      "spec_advance_iteration",
			Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": false, "reason": "a"},
		})
		assert.NoError(t, err)
	}
	_, err = sessionB.CallTool(ctx, &mcp.CallToolParams{
		Name:      "spec_advance_iteration",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals", "converged": false, "reason": "b"},
	})
	assert.NoError(t, err)

	stA, err := sessionA.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_status", Arguments: args})
	assert.NoError(t, err)
	stB, err := sessionB.CallTool(ctx, &mcp.CallToolParams{Name: "spec_loop_status", Arguments: args})
	assert.NoError(t, err)

	assert.Equal(t, 2, decodeLoopStatus(t, stA).Iteration, "session A advanced twice")
	assert.Equal(t, 1, decodeLoopStatus(t, stB).Iteration, "session B advanced once — counters must be independent")
}

// TestSessionTokens_DistinctPointersDistinctTokens is the direct
// unit-level proof of the token registry's contract, independent of the
// transport: distinct *ServerSession pointers get distinct stable
// tokens, the same pointer is stable across calls, and nil is handled.
func TestSessionTokens_DistinctPointersDistinctTokens(t *testing.T) {
	st := newSessionTokens()
	a := &mcp.ServerSession{}
	b := &mcp.ServerSession{}

	tokA1 := st.token(a)
	tokA2 := st.token(a)
	tokB := st.token(b)

	assert.Equal(t, tokA1, tokA2, "same pointer yields a stable token")
	assert.NotEqual(t, tokA1, tokB, "distinct pointers yield distinct tokens")
	assert.NotEmpty(t, st.token(nil), "nil session is handled with a constant token")
}

func TestSpecLoopStatus_NotStartedIsZero(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_status",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	out := decodeLoopStatus(t, res)
	assert.Equal(t, 0, out.Iteration)
	assert.False(t, out.Converged)
	assert.Equal(t, activity.DefaultMaxIterations, out.MaxIterations, "not-started status reports the cap that a begin would use")
}
