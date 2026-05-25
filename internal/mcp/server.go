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

	"github.com/chetan/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// implementationVersion is the version string advertised to MCP
// clients during capability negotiation. Kept as a constant rather
// than wired through cmd/buildinfo because the spec-server's identity
// (name + version) is independent of the CLI's release cadence — the
// server's surface is what clients reason about.
const implementationVersion = "dj-135-phase-1"

// NewSpecServer constructs a fully-configured *mcp.Server with the
// spec_* tool set, the spec://manifest resource, and the subscription
// handlers wired through. The SDK handles JSON-RPC dispatch and
// capability negotiation; this constructor's job is purely
// registration.
//
// Subscriptions: the SDK ignores resources/subscribe unless
// ServerOptions.SubscribeHandler is non-nil. We provide a no-op
// handler so the SDK accepts the subscription and tracks the session
// internally — that's all the spec://manifest surface needs, since
// the write tools call (*Server).ResourceUpdated which dispatches to
// every tracked subscriber regardless of which session originated the
// write. Multi-session coordination falls out naturally.
func NewSpecServer(store *agent.SpecStore) *mcp.Server {
	if store == nil {
		panic("mcp.NewSpecServer: store is required")
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus", Version: implementationVersion},
		&mcp.ServerOptions{
			SubscribeHandler:   func(_ context.Context, _ *mcp.SubscribeRequest) error { return nil },
			UnsubscribeHandler: func(_ context.Context, _ *mcp.UnsubscribeRequest) error { return nil },
		},
	)
	registerReadTools(server, store)
	registerWriteTools(server, store)
	registerResources(server, store)
	return server
}
