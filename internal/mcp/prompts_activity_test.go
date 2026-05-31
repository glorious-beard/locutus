package mcp

import (
	"context"
	"testing"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newServerWithPrompts spins up an in-memory MCP server wired with
// the activity-prompt surface. seedPlans lists the plan files to
// drop into .borg/plans/ before constructing the server; the
// registry is loaded from embedded defaults.
func newServerWithPrompts(t *testing.T, seedPlans map[string]string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/plans", 0o755))
	for name, body := range seedPlans {
		require.NoError(t, fsys.WriteFile(".borg/plans/"+name+".md", []byte(body), 0o644))
	}
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, reg, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

func TestActivityPrompts_RegistersOnePerPlanFile(t *testing.T) {
	session := newServerWithPrompts(t, map[string]string{
		"spec_refinement":   "spec_refinement playbook body",
		"feature_ingestion": "feature_ingestion playbook body",
		// code_adoption + code_assimilation deliberately omitted —
		// no plan file → no prompt.
	})

	res, err := session.ListPrompts(context.Background(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(res.Prompts))
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "spec_refinement")
	assert.Contains(t, names, "feature_ingestion")
	assert.NotContains(t, names, "code_adoption", "no plan file → no prompt")
}

func TestActivityPrompts_PromptsGetReturnsPlaybookAsUserMessage(t *testing.T) {
	session := newServerWithPrompts(t, map[string]string{
		"spec_refinement": "# Refine\n\nDispatch spec-scout.",
	})

	res, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name: "spec_refinement",
	})
	require.NoError(t, err)

	// One message, user role, content = playbook body. Q1 (b)
	// design: persona on Description, body on user message.
	require.Len(t, res.Messages, 1)
	assert.EqualValues(t, "user", res.Messages[0].Role)
	text, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok, "content should be TextContent; got %T", res.Messages[0].Content)
	assert.Contains(t, text.Text, "Dispatch spec-scout")
}

func TestActivityPrompts_PromptDescriptionCarriesPersona(t *testing.T) {
	session := newServerWithPrompts(t, map[string]string{
		"spec_refinement": "body",
	})

	res, err := session.ListPrompts(context.Background(), nil)
	require.NoError(t, err)

	var found *mcp.Prompt
	for _, p := range res.Prompts {
		if p.Name == "spec_refinement" {
			found = p
			break
		}
	}
	require.NotNil(t, found)
	assert.Contains(t, found.Description, "Locutus", "prompt description carries the persona")
	assert.Contains(t, found.Description, "spec_* tools")
}

func TestActivityPrompts_SkippedWhenNoPlansAtAll(t *testing.T) {
	session := newServerWithPrompts(t, map[string]string{})

	res, err := session.ListPrompts(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, res.Prompts, "no plan files → no prompts registered")
}
