package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// TestDispatchReAct_LoopExecutesToolThenReturnsFinalAnswer wires a
// two-iteration ReAct flow: the first model response emits one
// tool_call (echo), the dispatcher executes its handler, appends
// the result to the conversation, and loops; the second model
// response is the final text answer with no further tool_calls.
//
// Asserts:
//   - the handler was invoked exactly once with the model-emitted args
//   - the loop terminated on the second iteration with the final text
//   - the dispatcher routed convo correctly: iteration 2 saw the
//     tool result appended as a user-role message
//   - the accumulated AgentOutput carries one Round per iteration
func TestDispatchReAct_LoopExecutesToolThenReturnsFinalAnswer(t *testing.T) {
	var handlerCalls int32
	var lastArgs string
	echo := adapters.ToolDef{
		Name:        "echo",
		Description: "Returns its input verbatim",
		InputSchema: map[string]any{"type": "string"},
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			atomic.AddInt32(&handlerCalls, 1)
			lastArgs = string(input)
			result := map[string]string{"echoed": lastArgs}
			return json.Marshal(result)
		},
	}
	registry := NewToolRegistry()
	registry.Register(echo)

	mock := NewMockExecutor(
		// Iteration 1: model asks for the echo tool.
		MockResponse{Response: &AgentOutput{
			Content:   "Let me check via echo",
			ToolCalls: []ToolCall{{Name: "echo", Query: "hello", Status: "ok"}},
		}},
		// Iteration 2: model produces final answer.
		MockResponse{Response: &AgentOutput{
			Content: `{"final": "all done"}`,
		}},
	)

	def := AgentDef{
		ID:            "react_test",
		MaxIterations: 5,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	in := AgentInput{Messages: []Message{{Role: "user", Content: "kick off"}}}

	out, err := NewDispatcherWithTools(mock, registry).Dispatch(context.Background(), def, in, DispatchOptions{})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, `{"final": "all done"}`, out.Content,
		"final answer is the second-iteration content")
	assert.EqualValues(t, 1, atomic.LoadInt32(&handlerCalls),
		"handler invoked exactly once (one tool_call across two iterations)")
	assert.Equal(t, `"hello"`, lastArgs,
		"handler received the model-emitted query as a JSON string literal")

	require.Len(t, out.Rounds, 2, "one GenerateRound per ReAct iteration")
	assert.Equal(t, 1, out.Rounds[0].Index, "iteration numbering is 1-based")
	assert.Equal(t, 2, out.Rounds[1].Index)

	calls := mock.Calls()
	require.Len(t, calls, 2, "two adapter calls — one per iteration")

	// Iteration 1 sees just the user kickoff.
	assert.Equal(t, []Message{{Role: "user", Content: "kick off"}}, calls[0].Input.Messages)

	// Iteration 2 sees the kickoff + assistant's intermediate text +
	// the tool result as a user turn.
	require.Len(t, calls[1].Input.Messages, 3,
		"iteration 2 conversation = kickoff + assistant text + tool result")
	assert.Equal(t, "user", calls[1].Input.Messages[0].Role)
	assert.Equal(t, "kick off", calls[1].Input.Messages[0].Content)
	assert.Equal(t, "assistant", calls[1].Input.Messages[1].Role)
	assert.Equal(t, "Let me check via echo", calls[1].Input.Messages[1].Content)
	assert.Equal(t, "user", calls[1].Input.Messages[2].Role)
	assert.JSONEq(t, `{"echoed": "\"hello\""}`, calls[1].Input.Messages[2].Content,
		"tool result is the JSON the handler returned")
}

// TestDispatchReAct_NoToolCallsReturnsImmediately confirms that an
// agent with MaxIterations>1 still terminates on the first iteration
// when the model emits a final answer without tool_calls. The ReAct
// shape is only triggered by the field; agents that happen to not
// need tools on a particular call should pay no extra cost.
func TestDispatchReAct_NoToolCallsReturnsImmediately(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{
		Content: "direct answer",
	}})

	def := AgentDef{
		ID:            "react_test",
		MaxIterations: 5,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	in := AgentInput{Messages: []Message{{Role: "user", Content: "ping"}}}

	out, err := NewDispatcherWithTools(mock, NewToolRegistry()).Dispatch(context.Background(), def, in, DispatchOptions{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "direct answer", out.Content)
	assert.Equal(t, 1, mock.CallCount(),
		"ReAct loop must terminate on the first iteration when no tool_calls are emitted")
	require.Len(t, out.Rounds, 1, "single iteration still produces one Round")
}

// TestDispatchReAct_HitsMaxIterationsCap exhausts the loop by
// scripting the model to keep emitting tool_calls. The dispatcher
// must surface a "exceeded MaxIterations=N" error tagged with the
// agent id, and the handler must have been invoked once per iteration.
func TestDispatchReAct_HitsMaxIterationsCap(t *testing.T) {
	const cap = 3
	var handlerCalls int32
	noop := adapters.ToolDef{
		Name: "noop",
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			atomic.AddInt32(&handlerCalls, 1)
			return json.RawMessage(`{"ok": true}`), nil
		},
	}
	registry := NewToolRegistry()
	registry.Register(noop)

	// Script enough responses to exceed the cap. Each one emits a
	// tool_call, so the loop never terminates voluntarily.
	responses := make([]MockResponse, cap+2)
	for i := range responses {
		responses[i] = MockResponse{Response: &AgentOutput{
			ToolCalls: []ToolCall{{Name: "noop", Status: "ok"}},
		}}
	}
	mock := NewMockExecutor(responses...)

	def := AgentDef{
		ID:            "looper",
		MaxIterations: cap,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}

	out, err := NewDispatcherWithTools(mock, registry).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{})
	require.Error(t, err, "loop must error when no final answer arrives")
	assert.Contains(t, err.Error(), "exceeded MaxIterations=3",
		"error message includes the cap")
	assert.Contains(t, err.Error(), `agent "looper"`,
		"error message names the agent")
	require.NotNil(t, out, "accumulated output is returned for diagnosis")
	assert.Equal(t, cap, mock.CallCount(),
		"adapter is called exactly MaxIterations times")
	assert.EqualValues(t, cap, atomic.LoadInt32(&handlerCalls),
		"handler runs once per iteration")
}

// TestDispatchReAct_MultipleToolCallsInOneIteration verifies that
// when the model emits more than one tool_call in a single
// iteration, the dispatcher executes ALL handlers in order and
// appends ALL results to the conversation before re-calling the
// model. Stresses the inner loop's sequencing.
func TestDispatchReAct_MultipleToolCallsInOneIteration(t *testing.T) {
	var order []string
	makeTool := func(name string) adapters.ToolDef {
		return adapters.ToolDef{
			Name: name,
			Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
				order = append(order, name)
				return []byte(`{"name":"` + name + `"}`), nil
			},
		}
	}
	registry := NewToolRegistry()
	registry.Register(makeTool("alpha"))
	registry.Register(makeTool("beta"))
	registry.Register(makeTool("gamma"))

	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{
			Content: "checking three things",
			ToolCalls: []ToolCall{
				{Name: "alpha", Status: "ok"},
				{Name: "beta", Status: "ok"},
				{Name: "gamma", Status: "ok"},
			},
		}},
		MockResponse{Response: &AgentOutput{Content: "done"}},
	)

	def := AgentDef{
		ID:            "trio",
		MaxIterations: 3,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}

	out, err := NewDispatcherWithTools(mock, registry).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "done", out.Content)

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, order,
		"handlers ran in the model-emitted order")

	calls := mock.Calls()
	require.Len(t, calls, 2, "two adapter calls — first emitted three tool_calls, second was final")

	// Iteration 2's conversation: the assistant text + 3 tool result
	// turns. (No initial user message in this test.)
	require.Len(t, calls[1].Input.Messages, 4,
		"iteration 2 sees assistant text + three tool result messages")
	assert.Equal(t, "assistant", calls[1].Input.Messages[0].Role)
	for i, name := range []string{"alpha", "beta", "gamma"} {
		assert.Equal(t, "user", calls[1].Input.Messages[i+1].Role,
			"tool result %d is a user-role message", i+1)
		assert.JSONEq(t, `{"name":"`+name+`"}`, calls[1].Input.Messages[i+1].Content,
			"tool result %d carries the handler's JSON output verbatim", i+1)
	}
}

// TestDispatchReAct_MissingToolReturnsHandlerError covers the
// degenerate case where the model emits a tool_call for a name the
// dispatcher's registry doesn't know. The dispatcher should surface
// a clear error rather than silently appending an empty result.
func TestDispatchReAct_MissingToolReturnsHandlerError(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{
		ToolCalls: []ToolCall{{Name: "ghost", Status: "ok"}},
	}})

	def := AgentDef{
		ID:            "react_test",
		MaxIterations: 5,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}

	_, err := NewDispatcherWithTools(mock, NewToolRegistry()).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tool "ghost" not registered`,
		"error names the missing tool")
}

// TestDispatchReAct_SingleCallShapeUnchangedForLowMaxIterations
// confirms MaxIterations=0 and =1 keep the existing single-call
// dispatch shape (no ReAct scaffolding). This is the key
// non-regression: every existing agent has MaxIterations=0 (zero
// value); none of them should accidentally route through the new
// branch.
func TestDispatchReAct_SingleCallShapeUnchangedForLowMaxIterations(t *testing.T) {
	for _, maxIter := range []int{0, 1} {
		t.Run("max_iterations_"+itoa(maxIter), func(t *testing.T) {
			mock := NewMockExecutor(MockResponse{Response: &AgentOutput{
				Content:   "answer",
				ToolCalls: []ToolCall{{Name: "should_not_dispatch", Status: "ok"}},
			}})
			def := AgentDef{
				ID:            "single_call",
				MaxIterations: maxIter,
				Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
			}
			out, err := NewDispatcherWithTools(mock, NewToolRegistry()).Dispatch(
				context.Background(), def, AgentInput{}, DispatchOptions{})
			require.NoError(t, err)
			assert.Equal(t, "answer", out.Content)
			assert.Equal(t, 1, mock.CallCount(),
				"single-call shape: ToolCalls present in response are NOT followed up by the dispatcher")
		})
	}
}

// TestDispatchReAct_SafetyCeilingClampsLargeMaxIterations confirms
// the dispatcher caps iterations at reactSafetyCeiling even when
// the agent declares a much larger value. Belt-and-suspenders
// against a runaway config that would otherwise burn provider quota.
func TestDispatchReAct_SafetyCeilingClampsLargeMaxIterations(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(adapters.ToolDef{
		Name: "noop",
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	})

	// Always-tool-calls mock; the dispatcher should stop at the
	// safety ceiling regardless of the agent's declared cap.
	responses := make([]MockResponse, reactSafetyCeiling+5)
	for i := range responses {
		responses[i] = MockResponse{Response: &AgentOutput{
			ToolCalls: []ToolCall{{Name: "noop", Status: "ok"}},
		}}
	}
	mock := NewMockExecutor(responses...)

	def := AgentDef{
		ID:            "huge_cap",
		MaxIterations: reactSafetyCeiling + 10, // would loop forever
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}

	_, err := NewDispatcherWithTools(mock, registry).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"exceeded MaxIterations="+itoa(reactSafetyCeiling),
		"error message reports the clamped value, not the agent's declared value")
	assert.Equal(t, reactSafetyCeiling, mock.CallCount(),
		"adapter call count is capped at the safety ceiling")
}

// TestDispatchReAct_NoRegistrySurfacesError confirms an agent
// declaring MaxIterations>1 against a dispatcher constructed without
// a tool registry fails with a descriptive error rather than
// silently dropping the tool_call.
func TestDispatchReAct_NoRegistrySurfacesError(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{
		ToolCalls: []ToolCall{{Name: "anything", Status: "ok"}},
	}})

	def := AgentDef{
		ID:            "react_test",
		MaxIterations: 3,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}

	// Note: NewDispatcher(mock) leaves Tools nil because mock isn't
	// the concrete *Executor. ReAct on this dispatcher must error.
	_, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no tool registry"),
		"error explains the missing registry: %s", err.Error())
}

// itoa is a tiny helper to keep the test names readable without
// pulling strconv into every assertion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
