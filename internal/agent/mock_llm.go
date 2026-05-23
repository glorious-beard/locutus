package agent

import (
	"context"
	"sync"
)

// MockCall records one dispatch made through MockExecutor for test
// assertions. Captures the AgentDef and AgentInput exactly as the
// caller submitted them.
type MockCall struct {
	Def   AgentDef
	Input AgentInput
}

// MockResponse is a scripted output for MockExecutor. If Err is
// non-nil, Run returns the error instead of the output.
//
// AgentID, when set, scopes the response to a specific source
// agent: the mock matches against AgentDef.ID at call time and only
// serves agent-tagged responses to matching callers. Tagged and
// untagged responses can mix — tagged responses match only their
// agent; untagged responses fall back to positional consumption.
type MockResponse struct {
	AgentID  string
	Response *AgentOutput
	Err      error
	consumed bool
}

// MockExecutor implements AgentExecutor with scripted responses for
// testing. Responses are consumed in order; if exhausted, Run
// returns an error. All calls are recorded for assertion.
//
// DJ-130 retired the dispatcher-side thinking + schema split; the
// MockExecutor no longer needs a FormatProvider opt-in. Tests that
// want to exercise the split go through the per-adapter logic
// (internal/agent/adapters/*) rather than wiring a mock dispatcher.
type MockExecutor struct {
	mu        sync.Mutex
	responses []MockResponse
	calls     []MockCall
	pos       int

	// specStore is the unified spec store (DJ-134) the mock exposes
	// through SpecStore() so tests can drive GenerateSpec end-to-end
	// against an in-memory store without constructing a real *Executor
	// + adapter set. Nil by default; set via SetSpecStore only in
	// tests that exercise the RAG-tool surface.
	specStore *SpecStore
}

// NewMockExecutor creates a MockExecutor with the given scripted
// responses.
func NewMockExecutor(responses ...MockResponse) *MockExecutor {
	return &MockExecutor{responses: responses}
}

// Run returns the next scripted response, or an error if exhausted.
// Honors context cancellation by surfacing ErrTimeout — simulates
// the per-call deadline behavior production adapters implement.
func (m *MockExecutor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, ErrTimeout
	}

	if cb := AcquiredCallbackFromContext(ctx); cb != nil {
		cb()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, MockCall{Def: def, Input: input})

	// Tagged-response match takes precedence: a response with
	// AgentID set is served only to that agent. Lets parallel
	// tests script per-agent responses without depending on
	// goroutine arrival order.
	if def.ID != "" {
		for i := range m.responses {
			r := &m.responses[i]
			if r.consumed || r.AgentID == "" || r.AgentID != def.ID {
				continue
			}
			r.consumed = true
			if r.Err != nil {
				return nil, r.Err
			}
			return r.Response, nil
		}
	}

	for m.pos < len(m.responses) {
		r := &m.responses[m.pos]
		m.pos++
		if r.consumed {
			continue
		}
		if r.AgentID != "" && r.AgentID != def.ID {
			continue
		}
		r.consumed = true
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Response, nil
	}
	return nil, ErrTimeout
}

// Calls returns all recorded calls.
func (m *MockExecutor) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]MockCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// CallCount returns the number of calls made.
func (m *MockExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Reset clears all calls and resets the response position.
func (m *MockExecutor) Reset(responses ...MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = responses
	m.calls = nil
	m.pos = 0
}

// SetSpecStore wires the unified spec store (DJ-134) so tests can
// drive GenerateSpec end-to-end against an in-memory store. Test-only;
// production wires the store through *Executor.SetSpecStore.
func (m *MockExecutor) SetSpecStore(s *SpecStore) { m.specStore = s }

// SpecStore returns the wired store, or nil when none was set.
// Satisfies the structural interface the council's wrapper-chain
// lookup uses to find the store.
func (m *MockExecutor) SpecStore() *SpecStore { return m.specStore }
