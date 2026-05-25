package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// manifestURI is the canonical resource URI for the spec graph
// manifest. Subscribers to this URI receive
// notifications/resources/updated whenever a write tool commits.
const manifestURI = "spec://manifest"

// registerResources wires the spec://manifest resource. Per
// resolved-question 9 the v1 surface ships only the manifest;
// per-node resources are skipped — agents lookup individual bodies
// via spec_get instead.
//
// The resource's content is the same JSON that spec_list_manifest
// returns. Coding agents can attach the manifest as context (the
// resource pattern) or query it with the tool — both surfaces are
// kept consistent by sharing the same SpecStore.ListManifest call.
func registerResources(server *mcp.Server, store *agent.SpecStore) {
	server.AddResource(&mcp.Resource{
		URI:         manifestURI,
		Name:        "spec.manifest",
		Description: "The Locutus spec graph manifest — a compact JSON index of every spec node grouped by kind. Subscribe to receive notifications/resources/updated whenever the graph changes (a write tool commits).",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, err := json.Marshal(store.ListManifest())
		if err != nil {
			return nil, fmt.Errorf("marshal manifest: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      manifestURI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})
}
