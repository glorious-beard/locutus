package acp

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
	"github.com/glorious-beard/locutus/internal/dispatch/policy"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAgent satisfies acp.Agent so tests can wire one in-process via io.Pipe
// without spawning a subprocess. Behavior is scripted per-test by setting
// fields before openWithIO; defaults give a minimal initialize+session+prompt
// flow with no notifications, mirroring the smallest valid ACP exchange.
//
// The agent runs on its own goroutine (the SDK's read-loop), so all methods
// must be reentrant-safe. Tests that need to observe agent-side state should
// use atomics or check state only after closing the connection.
type fakeAgent struct {
	caps acpsdk.AgentCapabilities

	// onPrompt, if set, runs after the prompt is received and before we
	// return the PromptResponse. Lets tests script SessionUpdate
	// notifications and RequestPermission round-trips via the asc handle.
	asc      *acpsdk.AgentSideConnection
	onPrompt func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error)

	// onNewSession, if set, runs before NewSessionResponse is returned. Used
	// by the Phase 5 outgoing-meta tests to capture the request payload
	// (notably req.Meta) at the agent side.
	onNewSession func(req acpsdk.NewSessionRequest)

	sessionsCreated atomic.Int64
}

func (a *fakeAgent) Authenticate(context.Context, acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}

func (a *fakeAgent) Initialize(_ context.Context, req acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion:   req.ProtocolVersion,
		AgentCapabilities: a.caps,
	}, nil
}

func (a *fakeAgent) Cancel(context.Context, acpsdk.CancelNotification) error { return nil }

func (a *fakeAgent) CloseSession(context.Context, acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *fakeAgent) ResumeSession(context.Context, acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *fakeAgent) ListSessions(context.Context, acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}

func (a *fakeAgent) SetSessionMode(context.Context, acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, nil
}

func (a *fakeAgent) SetSessionConfigOption(context.Context, acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

func (a *fakeAgent) NewSession(_ context.Context, req acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	if a.onNewSession != nil {
		a.onNewSession(req)
	}
	n := a.sessionsCreated.Add(1)
	return acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId("sess_test_" + intToStr(n))}, nil
}

func (a *fakeAgent) Prompt(ctx context.Context, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	if a.onPrompt != nil {
		return a.onPrompt(ctx, a.asc, p)
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// wireFake spins up a fakeAgent on one end of a pair of pipes and returns the
// client-side reader/writer the Connection.openWithIO consumes. The agent
// connection lives until the test calls the returned cancel.
func wireFake(t *testing.T, agent *fakeAgent) (io.Writer, io.Reader, func()) {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()

	asc := acpsdk.NewAgentSideConnection(agent, agentToClientW, clientToAgentR)
	agent.asc = asc

	cancel := func() {
		_ = clientToAgentW.Close()
		_ = agentToClientR.Close()
	}
	return clientToAgentW, agentToClientR, cancel
}

func TestOpen_HappyPath(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	caps := conn.Capabilities()
	assert.True(t, caps.LoadSession, "agent capabilities should be cached from initialize response")
}

func TestNewSessionAndPrompt_AgentMessageChunk(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			// Emit one agent_message_chunk notification before returning.
			content := acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Type: "text", Text: "ok"}}
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
						SessionUpdate: "agent_message_chunk",
						Content:       content,
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
	require.True(t, strings.HasPrefix(sessionID, "sess_test_"), "session id should come from fakeAgent.NewSession")

	events, err := conn.Prompt(ctx, sessionID, "say ok", nil /* no permissions expected */)
	require.NoError(t, err)

	var got []dispatch.AgentEvent
	for ev := range events {
		got = append(got, ev)
	}
	require.GreaterOrEqual(t, len(got), 2, "expected at least one text event plus a terminal result event")

	// The terminal event is always last.
	terminal := got[len(got)-1]
	assert.Equal(t, dispatch.EventResult, terminal.Kind, "last event must be terminal Result kind")
	assert.Equal(t, string(acpsdk.StopReasonEndTurn), terminal.Text, "terminal event text should carry stopReason")

	// One of the earlier events should be the agent message chunk.
	var sawText bool
	for _, ev := range got[:len(got)-1] {
		if ev.Kind == dispatch.EventText && ev.Text == "ok" {
			sawText = true
			break
		}
	}
	assert.True(t, sawText, "expected an EventText carrying the agent_message_chunk content")
}

func TestPrompt_PolicyAllowOnce(t *testing.T) {
	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			// Mid-prompt, request permission. The client's Policy should
			// pick the allow_once option and we should see the agent's
			// continuation go through to end_turn.
			resp, err := asc.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
				SessionId: p.SessionId,
				ToolCall: acpsdk.ToolCallUpdate{
					ToolCallId: "tc1",
				},
				Options: []acpsdk.PermissionOption{
					{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow"},
					{OptionId: "deny", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject"},
				},
			})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow" {
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonRefusal}, nil
			}
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

	events, err := conn.Prompt(ctx, sessionID, "do a thing", policy.AllowOncePolicy{})
	require.NoError(t, err)

	var got []dispatch.AgentEvent
	for ev := range events {
		got = append(got, ev)
	}
	terminal := got[len(got)-1]
	assert.Equal(t, dispatch.EventResult, terminal.Kind)
	assert.Equal(t, string(acpsdk.StopReasonEndTurn), terminal.Text,
		"AllowOncePolicy should have selected the allow_once option; agent should reach end_turn")
}
