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
type MockExecutor struct {
	mu        sync.Mutex
	responses []MockResponse
	calls     []MockCall
	pos       int
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
