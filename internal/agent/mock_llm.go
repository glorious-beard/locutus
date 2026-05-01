package agent

import (
	"context"
	"sync"
)

// MockCall records a single call made to the mock LLM.
type MockCall struct {
	Request GenerateRequest
}

// MockResponse is a scripted response for the mock LLM. If Err is non-nil,
// Generate returns that error instead of the response.
//
// AgentID, when set, scopes the response to a specific source agent —
// the mock matches against AgentIDFromContext at call time and only
// serves agent-tagged responses to matching callers. This keeps tests
// deterministic when steps run in parallel (Phase 3 fanout fires
// concurrent goroutines whose mutex-acquisition order is non-
// deterministic; positional ordering would race). Mixing tagged and
// untagged responses works: tagged responses match only their agent;
// untagged responses fall back to positional consumption for callers
// with no agent id, or no matching tagged response.
type MockResponse struct {
	AgentID  string
	Response *GenerateResponse
	Err      error
	consumed bool
}

// MockLLM implements LLM with scripted responses for testing. Responses are
// consumed in order; if exhausted, Generate returns an error. All calls are
// recorded for assertion.
type MockLLM struct {
	mu        sync.Mutex
	responses []MockResponse
	calls     []MockCall
	pos       int
}

// NewMockLLM creates a MockLLM with the given scripted responses.
func NewMockLLM(responses ...MockResponse) *MockLLM {
	return &MockLLM{responses: responses}
}

// Generate returns the next scripted response, or an error if exhausted.
// It respects context cancellation/deadline.
func (m *MockLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	// Check context first — simulates timeout behavior.
	if err := ctx.Err(); err != nil {
		return nil, ErrTimeout
	}

	// Honor the acquired-callback contract real LLMs implement (see
	// GenKitLLM.Generate after acquireConcurrency). Workflow tests
	// exercising the queued → started transition need the mock to
	// invoke the callback at the moment the call "leaves the queue."
	// Mock has no semaphore, so we fire it immediately.
	if cb := AcquiredCallbackFromContext(ctx); cb != nil {
		cb()
	}

	agentID := AgentIDFromContext(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, MockCall{Request: req})

	// Agent-id-scoped match takes precedence: a response tagged with
	// AgentID is served only to that agent. Lets parallel tests script
	// a deterministic per-agent response without depending on the
	// goroutine arrival order at the mutex.
	if agentID != "" {
		for i := range m.responses {
			r := &m.responses[i]
			if r.consumed || r.AgentID == "" || r.AgentID != agentID {
				continue
			}
			r.consumed = true
			if r.Err != nil {
				return nil, r.Err
			}
			return r.Response, nil
		}
	}

	// Untagged responses fall back to positional consumption,
	// preserving existing test fixtures that don't care about
	// agent routing.
	for m.pos < len(m.responses) {
		r := &m.responses[m.pos]
		m.pos++
		if r.consumed {
			continue
		}
		if r.AgentID != "" && r.AgentID != agentID {
			// A different agent's tagged response — skip past it.
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
func (m *MockLLM) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]MockCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// CallCount returns the number of calls made.
func (m *MockLLM) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Reset clears all calls and resets the response position.
func (m *MockLLM) Reset(responses ...MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = responses
	m.calls = nil
	m.pos = 0
}
