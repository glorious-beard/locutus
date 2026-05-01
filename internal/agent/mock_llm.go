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
type MockResponse struct {
	Response *GenerateResponse
	Err      error
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

	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, MockCall{Request: req})

	if m.pos >= len(m.responses) {
		return nil, ErrTimeout
	}

	r := m.responses[m.pos]
	m.pos++

	if r.Err != nil {
		return nil, r.Err
	}
	return r.Response, nil
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
