package cmd

import (
	"context"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpProgressSession is the narrow slice of *mcp.ServerSession the progress
// wrapper uses. Defining it as a local interface lets tests swap in a
// mock without constructing a full SDK session.
type mcpProgressSession interface {
	NotifyProgress(ctx context.Context, params *mcp.ProgressNotificationParams) error
}

// sessionProgressNotifier translates dispatch.ProgressParams into MCP
// progress notifications. One notifier per tool invocation — the
// progress token is bound at construction so dispatch-layer callers
// don't have to know about it.
type sessionProgressNotifier struct {
	session mcpProgressSession
	token   any
}

// Notify forwards a dispatch-layer progress update to the MCP session.
// Errors propagate so retry/logging decisions stay with the caller; in
// practice Supervisor.emitProgress swallows them so a broken transport
// can't abort supervision.
func (n *sessionProgressNotifier) Notify(ctx context.Context, p dispatch.ProgressParams) error {
	return n.session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: n.token,
		Message:       p.Message,
		Progress:      float64(p.Current),
		Total:         float64(p.Total),
	})
}

// newProgressNotifier wraps an MCP session as a dispatch.ProgressNotifier
// bound to the given progress token. Returns nil when token is nil —
// the MCP spec says a client that doesn't pass a token isn't asking
// for progress, and dispatch.SupervisorConfig treats a nil notifier as
// "no forwarding."
func newProgressNotifier(session mcpProgressSession, token any) dispatch.ProgressNotifier {
	if token == nil {
		return nil
	}
	return &sessionProgressNotifier{session: session, token: token}
}

// progressNotifierFromRequest is the convenience helper tool handlers
// use: extract the progress token from the incoming CallToolRequest
// (if any) and wrap the session. Returns nil when the client didn't
// pass a token.
func progressNotifierFromRequest(req *mcp.CallToolRequest) dispatch.ProgressNotifier {
	if req == nil || req.Params == nil || req.Session == nil {
		return nil
	}
	return newProgressNotifier(req.Session, req.Params.GetProgressToken())
}
