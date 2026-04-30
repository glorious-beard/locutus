package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/chetan/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSink translates council events into MCP notifications/progress
// messages on the originating session. The progress token is captured
// at construction from the originating tool-call request — without
// one (the client opted out of progress) the sink no-ops, since the
// MCP spec forbids progress notifications without a matching token.
//
// Progress fields use a monotonically-increasing counter rather than
// a percentage because the council's total step count varies (revise
// is conditional on critic findings) and overpromising a total then
// missing it is worse than reporting open-ended progress.
type mcpSink struct {
	ctx     context.Context
	session *mcp.ServerSession
	token   any

	mu       sync.Mutex
	progress float64
}

// newMCPSink returns a sink bound to the request's session and token.
// Returns SilentSink when the request didn't include a progressToken,
// so the caller can use the same EventSink interface unconditionally.
func newMCPSink(ctx context.Context, req *mcp.CallToolRequest) agent.EventSink {
	if req == nil || req.Session == nil || req.Params == nil {
		return agent.SilentSink{}
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return agent.SilentSink{}
	}
	return &mcpSink{ctx: ctx, session: req.Session, token: token}
}

func (s *mcpSink) OnEvent(e agent.WorkflowEvent) {
	s.mu.Lock()
	s.progress++
	current := s.progress
	s.mu.Unlock()

	msg := fmt.Sprintf("%s · %s · %s", e.StepID, e.AgentID, e.Status)
	if e.Message != "" {
		msg = fmt.Sprintf("%s — %s", msg, e.Message)
	}
	// NotifyProgress error is best-effort: a broken session means the
	// client has gone away, and there's nothing useful to do at this
	// layer beyond letting the workflow finish on its own.
	_ = s.session.NotifyProgress(s.ctx, &mcp.ProgressNotificationParams{
		ProgressToken: s.token,
		Progress:      current,
		Message:       msg,
	})
}

func (s *mcpSink) Close() {}
