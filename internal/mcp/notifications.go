package mcp

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// publishManifestUpdate dispatches notifications/resources/updated for
// spec://manifest to every session subscribed to that URI. Called by
// each write tool handler after the SpecStore.Commit returns success.
//
// The SDK's *Server.ResourceUpdated handles the subscriber lookup
// (server-wide subscription table populated by the SubscribeHandler
// the spec-server wires through NewSpecServer). A session that wrote
// without first calling resources/subscribe receives nothing — that
// session simply isn't tracked. Cross-session coordination falls out
// naturally: client A subscribes, client B writes, client A is
// notified.
//
// Errors are logged at warn level rather than returned. A failed
// notification doesn't unwind a successful Commit — the write
// already happened, and the next manifest read will see the new
// state. Telling the caller "your write succeeded but I couldn't
// notify other clients" doesn't give the caller anything they could
// act on.
func publishManifestUpdate(ctx context.Context, server *mcp.Server) {
	err := server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{
		URI: manifestURI,
	})
	if err != nil {
		slog.Warn("mcp: publish manifest update failed", "uri", manifestURI, "error", err)
	}
}
