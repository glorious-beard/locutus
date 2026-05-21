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

	// specSearch is the optional SwappableSpecSearch the mock exposes
	// to specSearchSwap so tests can drive GenerateSpec end-to-end and
	// observe the council-scoped swap-and-restore behaviour without
	// constructing a real *Executor + adapter set. Nil by default; set
	// via SetSpecSearch only in tests that exercise the in-flight
	// spec_search wiring (DJ-123).
	specSearch *SwappableSpecSearch

	// specListManifest / specGet mirror specSearch for the DJ-125
	// list/get RAG-tool swappables. Tests that drive GenerateSpec
	// end-to-end and want to observe the in-flight redirection set
	// these via SetSpecListManifest / SetSpecGet; otherwise the
	// council's manifest/get swap path no-ops on the mock.
	specListManifest *SwappableSpecListManifest
	specGet          *SwappableSpecGet
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

// SetSpecSearch wires the swappable spec_search backend the council
// path swaps in and out via specSearchSwap. Test-only; production wires
// the swappable through *Executor.SetSpecSearch.
func (m *MockExecutor) SetSpecSearch(s *SwappableSpecSearch) { m.specSearch = s }

// SpecSearch returns the wired swappable, or nil when none was set.
// Satisfies the structural interface specSearchSwap looks for so a
// MockExecutor can exercise the in-flight swap-and-restore path without
// a real *Executor.
func (m *MockExecutor) SpecSearch() *SwappableSpecSearch { return m.specSearch }

// SetSpecListManifest / SpecListManifest mirror SetSpecSearch /
// SpecSearch for the DJ-125 spec_list_manifest swappable.
func (m *MockExecutor) SetSpecListManifest(s *SwappableSpecListManifest) { m.specListManifest = s }
func (m *MockExecutor) SpecListManifest() *SwappableSpecListManifest     { return m.specListManifest }

// SetSpecGet / SpecGet do the same for spec_get.
func (m *MockExecutor) SetSpecGet(s *SwappableSpecGet) { m.specGet = s }
func (m *MockExecutor) SpecGet() *SwappableSpecGet     { return m.specGet }
