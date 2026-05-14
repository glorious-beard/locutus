package acp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/dispatch/policy"
	acpsdk "github.com/coder/acp-go-sdk"
)

// activePrompt is the per-session state the client uses to route inbound
// notifications (session/update) and method calls (session/request_permission)
// back to the caller of Prompt. Created on Prompt entry, deleted on Prompt
// return. A non-existent entry on an inbound message means the prompt has
// already completed — defensive late-delivery handling.
type activePrompt struct {
	events chan<- dispatch.AgentEvent
	policy policy.Policy
}

// client implements acp.Client. It holds the per-session routing table and
// services the four method classes the protocol expects of a client:
//
//   - session/request_permission — translates the acpsdk payload into a
//     provider-neutral policy.Request and forwards to the active prompt's
//     policy.Policy.
//   - session/update notifications — translates and forwards to the active
//     prompt's events channel.
//   - fs/{read_text_file,write_text_file} — declined; we don't advertise the
//     capability in initialize, so a well-behaved agent never calls these.
//   - terminal/* — same posture as fs/*.
type client struct {
	mu     sync.RWMutex
	active map[acpsdk.SessionId]*activePrompt
}

func newClient() *client {
	return &client{active: make(map[acpsdk.SessionId]*activePrompt)}
}

// register associates the given activePrompt with the session id. An
// existing entry is replaced and a warning is logged — overlapping prompts
// on one session aren't part of the Phase 1 contract (the supervisor runs
// one prompt per session at a time) so this signals a caller bug.
func (c *client) register(sid acpsdk.SessionId, ap *activePrompt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.active[sid]; exists {
		slog.Warn("acp client: overlapping active prompts on one session", "session", sid)
	}
	c.active[sid] = ap
}

func (c *client) unregister(sid acpsdk.SessionId) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, sid)
}

func (c *client) lookup(sid acpsdk.SessionId) (*activePrompt, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ap, ok := c.active[sid]
	return ap, ok
}

// RequestPermission implements acp.Client. Adapts acpsdk's ToolCallUpdate
// + []PermissionOption into a provider-neutral policy.Request, invokes
// the active prompt's policy, and translates the policy.Decision back
// into the acpsdk response shape. Policy implementations therefore never
// import acpsdk.
func (c *client) RequestPermission(ctx context.Context, req acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	logInboundTraceparent("acp client: inbound session/request_permission",
		req.Meta,
		"session", req.SessionId,
		"tool_call", req.ToolCall.ToolCallId,
	)
	cancelled := acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
		},
	}
	ap, ok := c.lookup(req.SessionId)
	if !ok || ap.policy == nil {
		slog.Debug("acp client: permission request with no active prompt or policy; cancelling",
			"session", req.SessionId, "tool_call", req.ToolCall.ToolCallId)
		return cancelled, nil
	}

	policyReq := toPolicyRequest(req.ToolCall, req.Options)
	dec, err := ap.policy.Decide(ctx, policyReq)
	if err != nil {
		return acpsdk.RequestPermissionResponse{}, err
	}
	if dec.OptionID == "" {
		return cancelled, nil
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Selected: &acpsdk.RequestPermissionOutcomeSelected{
				Outcome:  "selected",
				OptionId: acpsdk.PermissionOptionId(dec.OptionID),
			},
		},
	}, nil
}

// toPolicyRequest converts acpsdk's permission-request payload into the
// provider-neutral policy.Request. Locations and Options are deep-copied
// so the Policy can hang on to them without aliasing SDK-owned memory.
func toPolicyRequest(tc acpsdk.ToolCallUpdate, opts []acpsdk.PermissionOption) policy.Request {
	locs := make([]policy.Location, 0, len(tc.Locations))
	for _, l := range tc.Locations {
		loc := policy.Location{Path: l.Path}
		if l.Line != nil {
			line := *l.Line
			loc.Line = &line
		}
		locs = append(locs, loc)
	}
	pOpts := make([]policy.Option, 0, len(opts))
	for _, o := range opts {
		pOpts = append(pOpts, policy.Option{
			OptionID: string(o.OptionId),
			Kind:     string(o.Kind),
			Name:     o.Name,
		})
	}
	kind := ""
	if tc.Kind != nil {
		kind = string(*tc.Kind)
	}
	title := ""
	if tc.Title != nil {
		title = *tc.Title
	}
	return policy.Request{
		ToolCallID: string(tc.ToolCallId),
		ToolName:   title,
		Kind:       kind,
		RawInput:   tc.RawInput,
		Locations:  locs,
		Options:    pOpts,
	}
}

// SessionUpdate implements acp.Client.
func (c *client) SessionUpdate(ctx context.Context, n acpsdk.SessionNotification) error {
	logInboundTraceparent("acp client: inbound session/update",
		n.Meta,
		"session", n.SessionId,
	)
	ap, ok := c.lookup(n.SessionId)
	if !ok {
		slog.Debug("acp client: session update with no active prompt; dropping",
			"session", n.SessionId)
		return nil
	}
	ev, skip := translateUpdate(n)
	if skip {
		return nil
	}
	select {
	case ap.events <- ev:
	case <-ctx.Done():
	}
	return nil
}

// WriteTextFile / ReadTextFile / terminal/* — all return errors because we
// declined to advertise the corresponding client capability. A well-behaved
// agent won't call them; a misbehaving agent gets a clean rejection rather
// than an unimplemented-method panic.

func (c *client) WriteTextFile(_ context.Context, _ acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, fmt.Errorf("fs.writeTextFile capability not advertised by this client")
}

func (c *client) ReadTextFile(_ context.Context, _ acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, fmt.Errorf("fs.readTextFile capability not advertised by this client")
}

func (c *client) CreateTerminal(_ context.Context, _ acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, fmt.Errorf("terminal capability not advertised by this client")
}

func (c *client) TerminalOutput(_ context.Context, _ acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, fmt.Errorf("terminal capability not advertised by this client")
}

func (c *client) ReleaseTerminal(_ context.Context, _ acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("terminal capability not advertised by this client")
}

func (c *client) WaitForTerminalExit(_ context.Context, _ acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("terminal capability not advertised by this client")
}

func (c *client) KillTerminal(_ context.Context, _ acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, fmt.Errorf("terminal capability not advertised by this client")
}
