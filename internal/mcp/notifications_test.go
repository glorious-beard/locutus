package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// newTestServerWithClientOptions is the notifications-test variant of
// newTestServer: it lets the test pass ClientOptions so the test can
// hook ResourceUpdatedHandler. Otherwise identical setup.
func newTestServerWithClientOptions(t *testing.T, opts *mcp.ClientOptions, seed func(*agent.SpecStore)) (*mcp.ClientSession, *agent.SpecStore) {
	t.Helper()
	ctx := context.Background()

	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	assert.NoError(t, err)
	if seed != nil {
		seed(store)
	}

	server := NewSpecServer(store, nil, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err = server.Connect(ctx, serverTransport, nil)
	assert.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, opts)
	session, err := client.Connect(ctx, clientTransport, nil)
	assert.NoError(t, err)

	t.Cleanup(func() { session.Close() })
	return session, store
}

func TestSpecServer_ResourceUpdated_FiresOnWrite(t *testing.T) {
	notified := make(chan string, 4)
	opts := &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			notified <- req.Params.URI
		},
	}
	session, _ := newTestServerWithClientOptions(t, opts, nil)

	// Subscribe BEFORE the write so the server tracks this session.
	err := session.Subscribe(context.Background(), &mcp.SubscribeParams{URI: "spec://manifest"})
	assert.NoError(t, err)

	// Trigger a write. spec_propose_feature requires at least one
	// existing decision, so seed via a propose_decision call first.
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "rationale",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	select {
	case uri := <-notified:
		assert.Equal(t, "spec://manifest", uri)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive notifications/resources/updated within timeout")
	}
}

func TestSpecServer_ResourceUpdated_SkippedWithoutSubscribe(t *testing.T) {
	notified := make(chan string, 4)
	opts := &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			notified <- req.Params.URI
		},
	}
	session, _ := newTestServerWithClientOptions(t, opts, nil)

	// NO subscribe call — the server should not notify this session.

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "rationale",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	// Give any erroneous notification a chance to land before declaring
	// the absence definitive. 250ms is well above the in-memory
	// transport's roundtrip cost.
	select {
	case uri := <-notified:
		t.Fatalf("unsubscribed client should not receive notifications; got %q", uri)
	case <-time.After(250 * time.Millisecond):
		// expected: nothing arrived
	}
}
