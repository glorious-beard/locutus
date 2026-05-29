// Package mcp implements Locutus's per-project Model Context Protocol
// server (DJ-135 Phase 1). The server exposes the spec graph through
// typed tools (spec_list_manifest, spec_get, spec_search,
// spec_propose_*, spec_revise_*) and the spec://manifest resource.
//
// The server runs as a singleton daemon per project, bound to a Unix
// socket under .locutus/mcp.sock. Multiple coding-agent processes
// (Claude Code, Codex, Gemini CLI) attach concurrently and share one
// SpecStore — write through one client is immediately visible to
// reads on another, and notifications/resources/updated fires on
// every subscribed session when the manifest changes.
package mcp

import (
	"context"
	"log/slog"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// implementationVersion is the version string advertised to MCP
// clients during capability negotiation. Kept as a constant rather
// than wired through cmd/buildinfo because the spec-server's identity
// (name + version) is independent of the CLI's release cadence — the
// server's surface is what clients reason about.
const implementationVersion = "dj-135-phase-1"

// NewSpecServer constructs a fully-configured *mcp.Server with the
// spec_* tool set, the spec://manifest resource, the subscription
// handlers wired through, and (when fsys + reg are provided) the
// activity-prompt surface that exposes published playbooks via
// prompts/get. The SDK handles JSON-RPC dispatch and capability
// negotiation; this constructor's job is purely registration.
//
// fsys and reg may be nil — useful for in-memory tests that only
// exercise the tool/resource surface. In production
// (cmd/mcp_daemon.go) both are supplied so the prompts surface is
// live alongside the tools.
//
// hist is the historian the write tools use to record audit events
// for irreversible operations (DJ-139 added the first such case: the
// goal-* / agoal-* delete tools). May be nil for tests and code paths
// that don't need history; nil-historian simply skips event recording
// on the write side. Production wiring in cmd/mcp_daemon.go supplies
// a real Historian rooted at .borg/history.
//
// Subscriptions: the SDK ignores resources/subscribe unless
// ServerOptions.SubscribeHandler is non-nil. We provide a no-op
// handler so the SDK accepts the subscription and tracks the session
// internally — that's all the spec://manifest surface needs, since
// the write tools call (*Server).ResourceUpdated which dispatches to
// every tracked subscriber regardless of which session originated the
// write. Multi-session coordination falls out naturally.
func NewSpecServer(store *agent.SpecStore, fsys specio.FS, reg *activity.Registry, hist *history.Historian) *mcp.Server {
	if store == nil {
		panic("mcp.NewSpecServer: store is required")
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus", Version: implementationVersion},
		&mcp.ServerOptions{
			SubscribeHandler:   func(_ context.Context, _ *mcp.SubscribeRequest) error { return nil },
			UnsubscribeHandler: func(_ context.Context, _ *mcp.UnsubscribeRequest) error { return nil },
			InitializedHandler: newInitializedHandler(slog.Default(), fsys),
		},
	)
	registerReadTools(server, store)
	registerWriteTools(server, store, hist)
	registerResources(server, store)
	// Interactive self-loop driver (DJ-142 phase 2). The loop store is
	// in-memory and per-daemon; the tools tolerate a nil reg (falling
	// back to activity.DefaultMaxIterations) so the test surface and
	// reg-less code paths still register them.
	registerLoopTools(server, newLoopStore(), reg)
	if fsys != nil && reg != nil {
		// Surface activity-playbook prompts. Errors here log via the
		// runtime; we don't fail server construction on a missing
		// plan file — the prompt simply isn't registered.
		_ = registerActivityPrompts(server, fsys, reg)
	}
	return server
}
