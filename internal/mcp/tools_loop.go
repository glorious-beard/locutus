package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool descriptions for the interactive self-loop driver (DJ-142
// phase 2). These three tools let a coding-agent runtime that lacks a
// native after-turn evaluator (Codex, Gemini) run its own convergence
// loop: begin a run, do an iteration of activity work, report the
// scout's verdict via advance, and keep looping while advance returns
// continue == true.
//
// Per DJ-134 the registered description is the agent-facing reference
// for what each tool does and the input shape it expects; the playbook
// supplies workflow guidance (when to reach for the tool), the
// description supplies behavior.
const (
	descSpecLoopBegin = "Begin (or recover) an interactive self-loop run. Pass activity (the activity you are executing, e.g. spec_refinement) and target (the target node id from your run context, e.g. goals); the pair plus your connection identifies this run's loop state, tracked server-side. Returns iteration (completed iterations so far — 0 for a fresh run) and max_iterations (the iteration ceiling for this activity, sourced from the registry, defaulting to 20). The count and cap are deterministic and owned by the server, so you never track them yourself. Call this once at the start of a run. After a context compression you can call it again with the same activity and target to recover the current iteration — it returns the in-flight count rather than resetting it."

	descSpecLoopStatus = "Read the current loop state for a run without advancing it. Pass the same activity and target you began with. Returns iteration (completed iterations), max_iterations (the ceiling), converged (the last reported verdict), and last_verdict (the one-line reason recorded with the last advance). Before any begin or advance, a run reports iteration 0 and converged false. Use this to inspect progress; use spec_advance_iteration to record an iteration result."

	descSpecAdvanceIteration = "Record the result of the iteration you just completed and learn whether to keep looping. Pass activity and target (the same pair you began with), converged (the scout's verdict: true when the spec graph has reached a fixed point, false when axes or concerns remain), and an optional one-line reason summarizing the verdict. Returns continue: keep running another iteration while it is true, and stop when it is false. The server stops the loop when the verdict is converged or when the iteration count reaches max_iterations, whichever comes first. Returns the incremented iteration count and the reason you supplied."
)

// loopBeginInput / loopStatusInput / advanceIterationInput carry the
// (activity, target) pair the agent re-derives from its run context on
// every call. Per the conventions, the schema descriptions name what
// the agent passes and that iteration + cap are server-tracked — the
// prompt does not re-explain shape.
type loopBeginInput struct {
	Activity string `json:"activity" jsonschema:"The activity being looped (e.g. spec_refinement). Re-derivable from your run context — pass it on every call so the server can locate this run's loop state."`
	Target   string `json:"target" jsonschema:"The target node id being refined (the Target: from your run context, e.g. goals). Combined with activity to identify the loop."`
}

type loopStatusInput struct {
	Activity string `json:"activity" jsonschema:"The activity being looped (e.g. spec_refinement). Re-derivable from your run context — pass it on every call so the server can locate this run's loop state."`
	Target   string `json:"target" jsonschema:"The target node id being refined (the Target: from your run context, e.g. goals). Combined with activity to identify the loop."`
}

type advanceIterationInput struct {
	Activity  string `json:"activity" jsonschema:"The activity being looped (e.g. spec_refinement). Re-derivable from your run context — pass it on every call so the server can locate this run's loop state."`
	Target    string `json:"target" jsonschema:"The target node id being refined (the Target: from your run context, e.g. goals). Combined with activity to identify the loop."`
	Converged bool   `json:"converged" jsonschema:"The scout's convergence verdict for the iteration just completed: true when the graph is at a fixed point, false when axes/concerns remain."`
	Reason    string `json:"reason,omitempty" jsonschema:"One-line summary of the verdict (recorded as last_verdict)."`
}

// loopBeginOutput / loopStatusOutput / advanceIterationOutput are the
// structured results the tools return so callers (and tests) can read
// the server-tracked state directly off StructuredContent.
type loopBeginOutput struct {
	Iteration     int `json:"iteration"`
	MaxIterations int `json:"max_iterations"`
}

type loopStatusOutput struct {
	Iteration     int    `json:"iteration"`
	MaxIterations int    `json:"max_iterations"`
	Converged     bool   `json:"converged"`
	LastVerdict   string `json:"last_verdict"`
}

type advanceIterationOutput struct {
	Continue  bool   `json:"continue"`
	Iteration int    `json:"iteration"`
	Reason    string `json:"reason,omitempty"`
}

// sessionTokens maps each per-connection *mcp.ServerSession to a stable
// string token so the loop store can scope runs by connection without
// the store importing MCP types. The shared per-project daemon hands
// each attached coding agent its own *ServerSession; assigning a stable
// "sess-N" per pointer keeps concurrent loops from colliding on the
// same (activity, target) pair.
type sessionTokens struct {
	mu  sync.Mutex
	ids map[*mcp.ServerSession]string
	seq uint64
}

func newSessionTokens() *sessionTokens {
	return &sessionTokens{ids: make(map[*mcp.ServerSession]string)}
}

// token returns the stable token for sess, assigning "sess-N" the first
// time a pointer is seen. A nil session (transports that don't populate
// req.Session) maps to a single shared constant so the tools still work.
func (s *sessionTokens) token(sess *mcp.ServerSession) string {
	if sess == nil {
		return "sess-none"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.ids[sess]; ok {
		return tok
	}
	s.seq++
	tok := fmt.Sprintf("sess-%d", s.seq)
	s.ids[sess] = tok
	return tok
}

// capFor resolves the iteration ceiling for an activity. A nil registry
// (in-memory tests) or an unknown activity falls back to
// activity.DefaultMaxIterations.
func capFor(reg *activity.Registry, activityName string) int {
	if reg == nil {
		return activity.DefaultMaxIterations
	}
	if a, ok := reg.Lookup(activityName); ok {
		return a.MaxIterations
	}
	return activity.DefaultMaxIterations
}

// registerLoopTools wires the three self-loop tools onto server. The
// loop store and registry are shared; the sessionTokens instance is
// owned here so tokens stay stable for the lifetime of the daemon.
func registerLoopTools(server *mcp.Server, ls *loopStore, reg *activity.Registry) {
	st := newSessionTokens()

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_loop_begin",
		Description: descSpecLoopBegin,
	}, func(_ context.Context, req *mcp.CallToolRequest, in loopBeginInput) (*mcp.CallToolResult, loopBeginOutput, error) {
		tok := st.token(req.Session)
		maxIter := capFor(reg, in.Activity)
		rec := ls.Begin(loopKey{tok, in.Activity, in.Target}, maxIter)
		return nil, loopBeginOutput{Iteration: rec.iteration, MaxIterations: rec.maxIter}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_loop_status",
		Description: descSpecLoopStatus,
	}, func(_ context.Context, req *mcp.CallToolRequest, in loopStatusInput) (*mcp.CallToolResult, loopStatusOutput, error) {
		tok := st.token(req.Session)
		rec, ok := ls.Status(loopKey{tok, in.Activity, in.Target})
		if !ok {
			// Not-started zero state: report the cap a begin would use
			// so the agent can reason about its budget before starting.
			return nil, loopStatusOutput{Iteration: 0, MaxIterations: capFor(reg, in.Activity)}, nil
		}
		return nil, loopStatusOutput{
			Iteration:     rec.iteration,
			MaxIterations: rec.maxIter,
			Converged:     rec.converged,
			LastVerdict:   rec.lastVerdict,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_advance_iteration",
		Description: descSpecAdvanceIteration,
	}, func(_ context.Context, req *mcp.CallToolRequest, in advanceIterationInput) (*mcp.CallToolResult, advanceIterationOutput, error) {
		tok := st.token(req.Session)
		rec, cont := ls.Advance(loopKey{tok, in.Activity, in.Target}, in.Converged, in.Reason)
		return nil, advanceIterationOutput{Continue: cont, Iteration: rec.iteration, Reason: in.Reason}, nil
	})
}
