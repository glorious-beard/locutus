package acp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/dispatch/policy"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract tests for the ACP transport. Where connection_test.go and
// meta_test.go cover specific features (initialize, message chunks,
// permission flow, traceparent), the tests here cover the full protocol
// contract the supervisor relies on under DJ-119:
//
//   - *Connection structurally satisfies dispatch.PromptConn
//   - Sequential prompts on one session work (spawn-per-workstream)
//   - Tool calls translate to EventToolCall with file paths
//   - Tool-call updates surface as EventToolResult ONLY for terminal
//     statuses; in-progress and pending updates are dropped
//   - Agent thought chunks surface as EventText
//   - Every StopReason variant produces a terminal EventResult with the
//     stopReason as Text
//   - Non-text content (image / audio) is dropped from the supervisor
//     event stream
//   - session/cancel propagates and surfaces as a cancelled terminal
//   - Late notifications (after the prompt has returned) don't crash
//   - Passing nil Policy to Prompt cancels permission requests

// TestContract_AcpConnectionSatisfiesPromptConn is a compile-time guard
// that *Connection satisfies dispatch.PromptConn. Without this assertion,
// a future signature change in either package could quietly break the
// structural-satisfaction contract that cmd/adopt.go's acpOpenConn
// relies on (it returns *Connection where dispatch.PromptConn is wanted).
func TestContract_AcpConnectionSatisfiesPromptConn(t *testing.T) {
	var _ dispatch.PromptConn = (*Connection)(nil)
}

// TestContract_SequentialPromptsOnOneSession verifies the DJ-119
// spawn-per-workstream lifecycle premise: opening one Connection and
// issuing multiple sequential Prompts on the same session must succeed
// and keep events routed to the correct caller.
func TestContract_SequentialPromptsOnOneSession(t *testing.T) {
	var promptCount atomic.Int64
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			n := promptCount.Add(1)
			text := "ok-" + intToStr(n)
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
						SessionUpdate: "agent_message_chunk",
						Content:       acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Type: "text", Text: text}},
					},
				},
			})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)

	for i := 1; i <= 3; i++ {
		events, err := conn.Prompt(ctx, sessionID, "say ok", nil)
		require.NoError(t, err, "Prompt %d", i)

		var sawText, sawTerminal bool
		want := "ok-" + intToStr(int64(i))
		for ev := range events {
			switch ev.Kind {
			case dispatch.EventText:
				if ev.Text == want {
					sawText = true
				}
			case dispatch.EventResult:
				sawTerminal = true
			}
		}
		assert.True(t, sawText, "Prompt %d should emit EventText %q", i, want)
		assert.True(t, sawTerminal, "Prompt %d should reach terminal EventResult", i)
	}
	assert.Equal(t, int64(3), promptCount.Load(), "agent should have received exactly 3 prompts on one session")
	assert.Equal(t, int64(1), agent.sessionsCreated.Load(), "all prompts share one session")
}

// TestContract_ToolCallEventTranslation verifies SessionUpdateToolCall
// → dispatch.EventToolCall translation, including title, input map,
// and file paths from locations.
func TestContract_ToolCallEventTranslation(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					ToolCall: &acpsdk.SessionUpdateToolCall{
						SessionUpdate: "tool_call",
						ToolCallId:    "tc-1",
						Title:         "Edit",
						Kind:          acpsdk.ToolKindEdit,
						RawInput:      map[string]any{"file_path": "src/main.go", "old": "a", "new": "b"},
						Locations:     []acpsdk.ToolCallLocation{{Path: "src/main.go"}},
					},
				},
			})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)

	events, err := conn.Prompt(ctx, sessionID, "edit a file", nil)
	require.NoError(t, err)

	var toolCall *dispatch.AgentEvent
	for ev := range events {
		ev := ev
		if ev.Kind == dispatch.EventToolCall {
			toolCall = &ev
		}
	}
	require.NotNil(t, toolCall, "expected an EventToolCall in the stream")
	assert.Equal(t, "Edit", toolCall.ToolName, "ToolName should come from ToolCall.Title")
	assert.Equal(t, "src/main.go", toolCall.ToolInput["file_path"], "RawInput map should pass through to ToolInput")
	assert.Equal(t, []string{"src/main.go"}, toolCall.FilePaths, "FilePaths should be lifted from Locations")
}

// TestContract_ToolCallUpdateTerminalStatusOnly verifies the translation
// layer's rule: status=completed/failed → EventToolResult; status=nil
// or in-progress/pending → dropped. This is what keeps the supervisor's
// monitor from being spammed with progress-only status updates.
func TestContract_ToolCallUpdateTerminalStatusOnly(t *testing.T) {
	completed := acpsdk.ToolCallStatusCompleted
	inProgress := acpsdk.ToolCallStatusInProgress
	pending := acpsdk.ToolCallStatusPending

	cases := []struct {
		name        string
		status      *acpsdk.ToolCallStatus
		wantSurface bool
	}{
		{"completed surfaces as EventToolResult", &completed, true},
		{"nil status dropped", nil, false},
		{"in_progress dropped", &inProgress, false},
		{"pending dropped", &pending, false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			agent := &fakeAgent{
				caps: acpsdk.AgentCapabilities{LoadSession: true},
				onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
					_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
						SessionId: p.SessionId,
						Update: acpsdk.SessionUpdate{
							ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
								SessionUpdate: "tool_call_update",
								ToolCallId:    "tc-1",
								Status:        c.status,
							},
						},
					})
					return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
				},
			}
			stdin, stdout, cancel := wireFake(t, agent)
			defer cancel()

			ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer ctxCancel()
			conn, err := openWithIO(ctx, stdin, stdout, "")
			require.NoError(t, err)
			defer conn.Close()

			sessionID, err := conn.NewSession(ctx, "/tmp")
			require.NoError(t, err)
			events, err := conn.Prompt(ctx, sessionID, "noop", nil)
			require.NoError(t, err)

			var sawToolResult bool
			for ev := range events {
				if ev.Kind == dispatch.EventToolResult {
					sawToolResult = true
				}
			}
			assert.Equal(t, c.wantSurface, sawToolResult, "EventToolResult surfacing for status=%v", c.status)
		})
	}
}

// TestContract_AgentThoughtChunkSurfacesAsText verifies the translation
// layer routes agent reasoning ("thought chunks") to EventText so the
// monitor's cycle-detection can see the agent's narration.
func TestContract_AgentThoughtChunkSurfacesAsText(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					AgentThoughtChunk: &acpsdk.SessionUpdateAgentThoughtChunk{
						SessionUpdate: "agent_thought_chunk",
						Content:       acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Type: "text", Text: "I should consider..."}},
					},
				},
			})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "think", nil)
	require.NoError(t, err)

	var sawThought bool
	for ev := range events {
		if ev.Kind == dispatch.EventText && strings.Contains(ev.Text, "consider") {
			sawThought = true
		}
	}
	assert.True(t, sawThought, "thought chunks should surface as EventText")
}

// TestContract_StopReasonVariantsCarryAsTerminalText verifies every
// StopReason variant the SDK declares produces a terminal EventResult
// with the stopReason in Text. The supervisor's validator inspects this
// field to distinguish end_turn (good) from refusal/max_tokens (bad).
func TestContract_StopReasonVariantsCarryAsTerminalText(t *testing.T) {
	variants := []acpsdk.StopReason{
		acpsdk.StopReasonEndTurn,
		acpsdk.StopReasonMaxTokens,
		acpsdk.StopReasonMaxTurnRequests,
		acpsdk.StopReasonRefusal,
		acpsdk.StopReasonCancelled,
	}

	for _, sr := range variants {
		sr := sr
		t.Run(string(sr), func(t *testing.T) {
			agent := &fakeAgent{
				caps: acpsdk.AgentCapabilities{LoadSession: true},
				onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
					return acpsdk.PromptResponse{StopReason: sr}, nil
				},
			}
			stdin, stdout, cancel := wireFake(t, agent)
			defer cancel()

			ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer ctxCancel()
			conn, err := openWithIO(ctx, stdin, stdout, "")
			require.NoError(t, err)
			defer conn.Close()

			sessionID, err := conn.NewSession(ctx, "/tmp")
			require.NoError(t, err)
			events, err := conn.Prompt(ctx, sessionID, "noop", nil)
			require.NoError(t, err)

			var terminal dispatch.AgentEvent
			var count int
			for ev := range events {
				terminal = ev
				count++
			}
			require.GreaterOrEqual(t, count, 1, "at least one event expected")
			assert.Equal(t, dispatch.EventResult, terminal.Kind)
			assert.Equal(t, string(sr), terminal.Text, "terminal Text should carry the stopReason verbatim")
		})
	}
}

// TestContract_NonTextContentDropped verifies image/audio agent message
// chunks are dropped from the supervisor event stream (preserved on
// Raw for archival, but not surfaced as EventText). This is what keeps
// the validator's input clean of binary blobs.
func TestContract_NonTextContentDropped(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			// Emit an image content block — has no Text field.
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
						SessionUpdate: "agent_message_chunk",
						Content: acpsdk.ContentBlock{Image: &acpsdk.ContentBlockImage{
							Type:     "image",
							Data:     "BASE64DATA",
							MimeType: "image/png",
						}},
					},
				},
			})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "show me an image", nil)
	require.NoError(t, err)

	var textEvents int
	var terminal dispatch.AgentEvent
	for ev := range events {
		if ev.Kind == dispatch.EventText {
			textEvents++
		}
		terminal = ev
	}
	assert.Equal(t, 0, textEvents, "image content should NOT surface as EventText")
	assert.Equal(t, dispatch.EventResult, terminal.Kind, "still get the terminal")
}

// TestContract_SessionCancelPropagates verifies conn.Cancel triggers a
// session/cancel that the SDK exchange surfaces as a cancelled
// stopReason. The terminal event carries "cancelled" as Text so the
// supervisor's runAttempt can distinguish ctx-cancel from natural
// end-of-turn.
func TestContract_SessionCancelPropagates(t *testing.T) {
	promptStarted := make(chan struct{})
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			close(promptStarted)
			// Block until the cancel notification reaches the SDK and ctx is done.
			<-ctx.Done()
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "block", nil)
	require.NoError(t, err)

	// Wait for the agent to be inside Prompt, then send session/cancel.
	select {
	case <-promptStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("agent never reached onPrompt")
	}
	require.NoError(t, conn.Cancel(context.Background(), sessionID))

	var terminal dispatch.AgentEvent
	for ev := range events {
		terminal = ev
	}
	assert.Equal(t, dispatch.EventResult, terminal.Kind, "must reach a terminal event after cancel")
	assert.Equal(t, string(acpsdk.StopReasonCancelled), terminal.Text,
		"cancelled stopReason should surface as terminal Text")
}

// TestContract_LateNotificationAfterPromptComplete verifies the client's
// defensive late-delivery handling. A SessionUpdate arriving AFTER the
// prompt's events channel has closed must not panic; it should be
// dropped silently (the client logs at debug level but the test doesn't
// assert on logs).
func TestContract_LateNotificationAfterPromptComplete(t *testing.T) {
	emitLate := make(chan struct{})
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			// Schedule a late notification that fires after Prompt returns.
			go func() {
				<-emitLate
				_ = asc.SessionUpdate(context.Background(), acpsdk.SessionNotification{
					SessionId: p.SessionId,
					Update: acpsdk.SessionUpdate{
						AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
							SessionUpdate: "agent_message_chunk",
							Content:       acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Type: "text", Text: "late"}},
						},
					},
				})
			}()
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "do it", nil)
	require.NoError(t, err)

	for range events {
		// Drain.
	}
	// Now the active-prompt entry is unregistered. Fire the late notification;
	// the client should drop it silently. If it panics or blocks, the test
	// times out.
	close(emitLate)
	// Give the goroutine a beat to deliver. No assertion — we're testing
	// that no panic/hang occurs.
	time.Sleep(100 * time.Millisecond)
}

// TestContract_NilPolicyCancelsPermissionRequest verifies that passing
// nil to conn.Prompt as the Policy results in any permission request
// being cancelled. This is the defensive default the supervisor uses
// when no Policy has been wired (and the documented semantics of nil
// in connection.go).
func TestContract_NilPolicyCancelsPermissionRequest(t *testing.T) {
	var permResp atomic.Value // acpsdk.RequestPermissionResponse
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			resp, err := asc.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
				SessionId: p.SessionId,
				ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "tc1"},
				Options: []acpsdk.PermissionOption{
					{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow"},
				},
			})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			permResp.Store(resp)
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonRefusal}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)

	// Pass nil Policy — the client should cancel any permission request.
	events, err := conn.Prompt(ctx, sessionID, "ask permission", nil)
	require.NoError(t, err)
	for range events {
	}

	got := permResp.Load().(acpsdk.RequestPermissionResponse)
	require.NotNil(t, got.Outcome.Cancelled, "nil policy should produce a cancelled outcome")
}

// TestContract_AllowOncePolicySelectsCorrectOption is the
// counterpart to NilPolicy — verifies the dispatch/policy.AllowOncePolicy
// (the test placeholder + Phase 3 default) picks the first allow_once
// option even when the agent offers it second in the list.
func TestContract_AllowOncePolicySelectsCorrectOption(t *testing.T) {
	var permResp atomic.Value
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			resp, err := asc.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
				SessionId: p.SessionId,
				ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "tc1"},
				// Reject options listed first; allow_once second. AllowOncePolicy
				// scans the whole list and picks any allow option.
				Options: []acpsdk.PermissionOption{
					{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject"},
					{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow"},
				},
			})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			permResp.Store(resp)
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "ask", policy.AllowOncePolicy{})
	require.NoError(t, err)
	for range events {
	}

	got := permResp.Load().(acpsdk.RequestPermissionResponse)
	require.NotNil(t, got.Outcome.Selected, "allow policy should produce a selected outcome")
	assert.Equal(t, acpsdk.PermissionOptionId("allow"), got.Outcome.Selected.OptionId,
		"AllowOncePolicy should pick the allow option regardless of ordering")
}
