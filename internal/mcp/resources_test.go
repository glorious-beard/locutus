package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

func TestSpecServer_ResourceManifest_RendersFullGraph(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
		seedFeature(store, "feat-dashboard", "Realtime dashboard", "dec-storage")
	})

	res, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "spec://manifest",
	})
	assert.NoError(t, err)
	assert.Len(t, res.Contents, 1)
	assert.Equal(t, "spec://manifest", res.Contents[0].URI)
	assert.Equal(t, "application/json", res.Contents[0].MIMEType)

	var fromResource agent.SpecManifest
	assert.NoError(t, json.Unmarshal([]byte(res.Contents[0].Text), &fromResource))

	// Cross-check against the tool surface — both should return the
	// same content. The resource and tool are two ways to read the
	// same SpecStore; if they diverge we've added implicit
	// caching/transformation that subverts the singleton coordination
	// story.
	toolRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_list_manifest"})
	assert.NoError(t, err)
	raw, err := json.Marshal(toolRes.StructuredContent)
	assert.NoError(t, err)
	var fromTool agent.SpecManifest
	assert.NoError(t, json.Unmarshal(raw, &fromTool))

	assert.Equal(t, fromTool, fromResource, "resources/read and spec_list_manifest should return identical content")
}
