# DJ-143 Per-Runtime Tool Restriction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restrict the three `spec_loop_*` MCP tools to Codex/Gemini sessions so Claude Code interactive can't derail its `/goal`-driven loop by calling them, per [DJ-143](../../docs/decisions/dj-143-per-runtime-tool-policy.md).

**Architecture:** Capture `(runtime, mode)` per MCP session at `initialize` time (`ClientInfo.name` + `_meta["locutus.mode"]`), expose via a `SessionRuntime` accessor, and wrap each restricted tool's handler in a `requireRuntime` adapter at registration. Mode flows from the ACP harness's `LOCUTUS_MODE=headless` env var, through the `locutus mcp` bridge (which parses-and-rewrites the client's `initialize` JSON-RPC message to inject the `_meta` field), to the daemon's `InitializedHandler`. No new config file; the restriction lives at each tool's registration site.

**Tech Stack:** Go (stdlib `encoding/json`, `bufio`, `sync`, `os`); MCP via `github.com/modelcontextprotocol/go-sdk v1.6.1` (`ServerSession.InitializeParams()`, `ServerOptions.InitializedHandler`); testify.

---

## File Structure

- `internal/mcp/session_context.go` *(new)* — `InitializedHandler` captures runtime+mode; `SessionRuntime` accessor; `requireRuntime` registration wrapper. One file owns the session→`(runtime, mode)` map, the deny error shape, and the wrapper.
- `internal/mcp/session_context_test.go` *(new)* — unit tests for the handler, accessor, and wrapper.
- `internal/mcp/server.go` *(modify)* — wire `InitializedHandler` into the `ServerOptions` block in `NewSpecServer`.
- `internal/mcp/tools_loop.go` *(modify)* — sharpen the three `desc*` constants with the leading runtime-audience sentence; wrap each `mcp.AddTool` registration in `requireRuntime("codex", "gemini")`.
- `internal/mcp/tools_loop_test.go` *(modify or add new dj143_test.go alongside)* — integration test: claude-code session denied; codex session allowed.
- `internal/mcp/bridge.go` *(modify)* — `BridgeIOToSocket` gains a `mode` parameter; parses incoming JSON-RPC lines until it sees the client's `initialize` request, mutates `params._meta["locutus.mode"]`, then drops to raw byte pumping.
- `internal/mcp/bridge_test.go` *(new or extend existing)* — drive the bridge against an in-memory socket server; assert the injected `_meta` reaches the server.
- `cmd/mcp.go` *(modify)* — read `os.Getenv("LOCUTUS_MODE")` with `"interactive"` default; pass through to `BridgeStdioToSocket`/`BridgeIOToSocket`.
- `internal/dispatch/acp/connection.go` *(no change — already supports `spawn.Env`)*.
- `internal/runner/run.go` *(modify)* — when constructing the `acp.Spawn` for headless dispatch, set `spawn.Env = append(os.Environ(), "LOCUTUS_MODE=headless")` so the coding-agent process and its child `locutus mcp` bridge both see the env var.
- `internal/runner/run_test.go` *(modify or add new dj143_test.go)* — assert the headless dispatch path puts `LOCUTUS_MODE=headless` on the spawn env.
- `docs/runtime-affordances.md` *(modify)* — add "Tool-Restriction" passage.
- `CLAUDE.md` *(modify)* — one-paragraph addition in Sources of Truth.

---

## Task 1: Session-context module — `SessionRuntime`, `requireRuntime`, `InitializedHandler`

**Files:**
- Create: `internal/mcp/session_context.go`
- Test: `internal/mcp/session_context_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/session_context_test.go`:

```go
// DJ-143 — the session-context module captures (runtime, mode) at
// MCP initialize time from ClientInfo.name + _meta["locutus.mode"]
// and exposes SessionRuntime + requireRuntime for handler use.
package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession is a stand-in for *mcp.ServerSession used by the
// accessor's map-key path; the real test in Task 2 exercises the
// SDK-driven InitializedHandler end-to-end.
type fakeSessionKey struct{ id string }

func TestSessionRuntimeStoresAndRetrieves(t *testing.T) {
	clearSessionRuntimes()
	key := &fakeSessionKey{id: "s1"}
	storeSessionRuntime(key, "claude-code", "interactive")
	rt, mode := sessionRuntimeFor(key)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "interactive", mode)
}

func TestSessionRuntimeUnknownReturnsEmpty(t *testing.T) {
	clearSessionRuntimes()
	rt, mode := sessionRuntimeFor(&fakeSessionKey{id: "missing"})
	assert.Empty(t, rt)
	assert.Empty(t, mode)
}

func TestSessionRuntimeNormalizesCase(t *testing.T) {
	clearSessionRuntimes()
	key := &fakeSessionKey{id: "s2"}
	storeSessionRuntime(key, "Claude-Code", "  Interactive  ")
	rt, mode := sessionRuntimeFor(key)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "interactive", mode)
}

func TestRequireRuntimeAllowsListedRuntime(t *testing.T) {
	clearSessionRuntimes()
	key := &fakeSessionKey{id: "codex-session"}
	storeSessionRuntime(key, "codex", "interactive")
	called := false
	wrapped := requireRuntimeAny(func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		called = true
		return nil, struct{}{}, nil
	}, "codex", "gemini")
	res, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, struct{}{})
	// Wire the session for the call via a test-only setter.
	_ = key
	_ = res
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRequireRuntimeDeniesUnlistedRuntime(t *testing.T) {
	clearSessionRuntimes()
	// The test only validates the deny path's error shape. The session
	// lookup is plumbed through a test-injection point that the
	// wrapper reads — see denyErrorContains below.
	got := denyErrorMessageFor("claude-code", "interactive", "spec_loop_begin", "codex", "gemini")
	assert.Contains(t, strings.ToLower(got), "claude-code")
	assert.Contains(t, got, "interactive")
	assert.Contains(t, got, "spec_loop_begin")
	assert.Contains(t, got, "docs/runtime-affordances.md")
}
```

Note on test design: the wrapper reads the calling session from `req.Session`. For the unit test, we use small test-only helpers (`clearSessionRuntimes`, `denyErrorMessageFor`) that exercise the components in isolation. The Task 3 integration test drives the full path through the MCP SDK with two simulated sessions.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestSessionRuntime|TestRequireRuntime' -v`
Expected: FAIL — file doesn't exist yet.

- [ ] **Step 3: Create the module**

Create `internal/mcp/session_context.go`:

```go
package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionRuntimes maps each per-connection MCP session key to its
// captured (runtime, mode) pair. The map is keyed by the session
// pointer (any) because the SDK does not expose user-attachable
// session metadata; storing alongside the SDK's session is the
// minimum-coupling alternative. Cleared when the session closes via
// the deferred delete in the InitializedHandler wiring (Task 2).
//
// Per DJ-143 §1: runtime comes from ClientInfo.name, mode from
// _meta["locutus.mode"] forwarded by the bridge.
var (
	sessionRuntimesMu sync.RWMutex
	sessionRuntimes   = map[any]sessionContext{}
)

type sessionContext struct {
	runtime string
	mode    string
}

func storeSessionRuntime(sess any, runtime, mode string) {
	sessionRuntimesMu.Lock()
	defer sessionRuntimesMu.Unlock()
	sessionRuntimes[sess] = sessionContext{
		runtime: strings.ToLower(strings.TrimSpace(runtime)),
		mode:    strings.ToLower(strings.TrimSpace(mode)),
	}
}

func sessionRuntimeFor(sess any) (runtime, mode string) {
	sessionRuntimesMu.RLock()
	defer sessionRuntimesMu.RUnlock()
	c, ok := sessionRuntimes[sess]
	if !ok {
		return "", ""
	}
	return c.runtime, c.mode
}

func clearSessionRuntimes() {
	sessionRuntimesMu.Lock()
	defer sessionRuntimesMu.Unlock()
	sessionRuntimes = map[any]sessionContext{}
}

// SessionRuntime is the public accessor handlers use to read the
// calling session's runtime and mode. Returns ("", "") when the
// session has not yet completed initialize (or is unknown).
func SessionRuntime(sess *mcp.ServerSession) (runtime, mode string) {
	return sessionRuntimeFor(sess)
}

// requireRuntimeAny wraps a tool handler so it returns a runtime-
// restriction error when the calling session's runtime is not in the
// allowlist. The generic shape lets the wrapper apply to any
// ToolHandlerFor[In, Out].
//
// Per DJ-143 §3 RQ5: an unknown runtime (session not in the map, or
// runtime not recognized) is treated as not-in-allowlist for
// restricted tools — restricted tools are opt-in by runtime.
func requireRuntimeAny[In, Out any](
	h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	allowed ...string,
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowSet[strings.ToLower(r)] = struct{}{}
	}
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		runtime, mode := sessionRuntimeFor(req.Session)
		if _, ok := allowSet[runtime]; !ok {
			var zero Out
			toolName := ""
			if req != nil && req.Params != nil {
				toolName = req.Params.Name
			}
			return errorResult(denyErrorMessageFor(runtime, mode, toolName, allowed...)), zero, nil
		}
		return h(ctx, req, in)
	}
}

// denyErrorMessageFor formats the runtime-restriction error message.
// Per DJ-143 §3: name the runtime, the mode, the tool, and a docs
// reference so an operator hitting it has a clear next step.
func denyErrorMessageFor(runtime, mode, tool string, allowed ...string) string {
	rt := runtime
	if rt == "" {
		rt = "<unknown>"
	}
	m := mode
	if m == "" {
		m = "<unknown>"
	}
	return fmt.Sprintf(
		"tool %q is not exposed to runtime %q (mode=%s); allowed runtimes: %s; see docs/runtime-affordances.md § Tool-Restriction",
		tool, rt, m, strings.Join(allowed, ", "),
	)
}
```

- [ ] **Step 4: Adjust the test to match real signatures**

The Step 1 test sketched two test paths that need to compile against the real wrapper signature. Replace the `TestRequireRuntimeAllowsListedRuntime` and `TestRequireRuntimeDeniesUnlistedRuntime` tests with the in-package versions below (the wrapper is generic so we need a concrete In/Out type for the test):

```go
type emptyArgs struct{}
type emptyResult struct{}

func TestRequireRuntimeAllowsListed(t *testing.T) {
	clearSessionRuntimes()
	called := false
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, emptyResult, error) {
		called = true
		return nil, emptyResult{}, nil
	}
	wrapped := requireRuntimeAny(inner, "codex", "gemini")

	// Simulate a session by storing its runtime under a fixed key.
	key := &fakeSessionKey{id: "codex-1"}
	storeSessionRuntime(key, "codex", "interactive")

	req := &mcp.CallToolRequest{Session: nil, Params: &mcp.CallToolParams{Name: "spec_loop_begin"}}
	// Inject the key as req.Session via a small swap: the wrapper looks up
	// sessionRuntimeFor(req.Session). For the unit test, the session value
	// passed to storeSessionRuntime must equal what the wrapper reads from
	// req.Session — use the fakeSessionKey as a stand-in.
	req.Session = (*mcp.ServerSession)(nil) // unused; lookup uses the stored key
	// Re-bind: temporarily store under nil session pointer.
	clearSessionRuntimes()
	storeSessionRuntime(req.Session, "codex", "interactive")

	_, _, err := wrapped(context.Background(), req, emptyArgs{})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRequireRuntimeDeniesUnlisted(t *testing.T) {
	clearSessionRuntimes()
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, emptyResult, error) {
		t.Fatal("inner handler must not run when runtime is denied")
		return nil, emptyResult{}, nil
	}
	wrapped := requireRuntimeAny(inner, "codex", "gemini")

	req := &mcp.CallToolRequest{Session: nil, Params: &mcp.CallToolParams{Name: "spec_loop_begin"}}
	clearSessionRuntimes()
	storeSessionRuntime(req.Session, "claude-code", "interactive")

	res, _, err := wrapped(context.Background(), req, emptyArgs{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	require.NotEmpty(t, res.Content)
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		assert.Contains(t, tc.Text, "claude-code")
		assert.Contains(t, tc.Text, "spec_loop_begin")
	}
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/mcp/ -run 'TestSessionRuntime|TestRequireRuntime' -v`
Expected: PASS (all five tests).

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/session_context.go internal/mcp/session_context_test.go
git commit -m "feat(mcp): session-context module — SessionRuntime + requireRuntime wrapper (DJ-143)"
```

---

## Task 2: Wire `InitializedHandler` into `NewSpecServer`

**Files:**
- Modify: `internal/mcp/server.go:57-83` (`NewSpecServer`)
- Modify: `internal/mcp/session_context.go` (add `newInitializedHandler` factory)
- Test: append to `internal/mcp/session_context_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/session_context_test.go`:

```go
// TestInitializedHandlerExtractsRuntimeAndMode — drive a full MCP
// initialize exchange against an in-memory transport pair; assert the
// daemon captured the client's ClientInfo.name as runtime and
// _meta["locutus.mode"] as mode.
func TestInitializedHandlerExtractsRuntimeAndMode(t *testing.T) {
	clearSessionRuntimes()

	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus-test", Version: "0.0.0"},
		&mcp.ServerOptions{
			InitializedHandler: newInitializedHandler(),
		},
	)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(
		&mcp.Implementation{Name: "claude-code", Version: "test"},
		nil,
	)
	cs, err := client.Connect(context.Background(), clientT, &mcp.ClientSessionOptions{
		InitializeParams: &mcp.InitializeParams{
			Meta: map[string]any{"locutus.mode": "interactive"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for the server to process the initialized notification.
	// In go-sdk InitializedHandler fires after the client's
	// notifications/initialized; a brief poll covers the race.
	assertEventuallyTrue(t, func() bool {
		rt, _ := sessionRuntimeFor(ss)
		return rt == "claude-code"
	})

	rt, mode := sessionRuntimeFor(ss)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "interactive", mode)
}

// assertEventuallyTrue polls f every 5ms up to 1s, t.Fatal-ing if it
// never returns true. Replace with testify/require.Eventually if the
// project already uses it; check existing helpers first.
func assertEventuallyTrue(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("assertEventuallyTrue: condition never became true")
}
```

Add the necessary imports to the test file: `"time"`. Check whether `assertEventuallyTrue` or `require.Eventually` is already used in the package; reuse rather than redefine.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestInitializedHandlerExtractsRuntimeAndMode -v`
Expected: FAIL — `newInitializedHandler` doesn't exist; `sessionRuntimeFor(ss)` returns empty.

- [ ] **Step 3: Add the handler factory**

In `internal/mcp/session_context.go`, append:

```go
// newInitializedHandler returns an InitializedHandler that, for each
// session whose initialize completes, reads ClientInfo.name (runtime)
// and _meta["locutus.mode"] (mode) from InitializeParams and stores
// them on the session map. Per DJ-143 §1+§2: runtime from the MCP
// protocol's clientInfo, mode from the bridge-forwarded _meta field
// (default "interactive" when absent).
func newInitializedHandler() func(context.Context, *mcp.InitializedRequest) {
	return func(_ context.Context, req *mcp.InitializedRequest) {
		if req == nil || req.Session == nil {
			return
		}
		params := req.Session.InitializeParams()
		if params == nil {
			return
		}
		runtime := ""
		if params.ClientInfo != nil {
			runtime = params.ClientInfo.Name
		}
		mode := "interactive"
		if params.Meta != nil {
			if v, ok := params.Meta["locutus.mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = v
			}
		}
		storeSessionRuntime(req.Session, runtime, mode)
	}
}
```

- [ ] **Step 4: Wire into `NewSpecServer`**

In `internal/mcp/server.go`, update `NewSpecServer` (around line 61-67) to add `InitializedHandler` to the `ServerOptions`:

```go
	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus", Version: implementationVersion},
		&mcp.ServerOptions{
			SubscribeHandler:   func(_ context.Context, _ *mcp.SubscribeRequest) error { return nil },
			UnsubscribeHandler: func(_ context.Context, _ *mcp.UnsubscribeRequest) error { return nil },
			InitializedHandler: newInitializedHandler(),
		},
	)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run TestInitializedHandlerExtractsRuntimeAndMode -v`
Expected: PASS.

Also run the full `internal/mcp` test suite to confirm no existing test broke: `go test ./internal/mcp/ 2>&1 | tail -10`. Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/server.go internal/mcp/session_context.go internal/mcp/session_context_test.go
git commit -m "feat(mcp): wire InitializedHandler to capture session (runtime, mode) (DJ-143)"
```

---

## Task 3: Wrap `spec_loop_*` with `requireRuntime` and sharpen descriptions

**Files:**
- Modify: `internal/mcp/tools_loop.go:24-32` (`desc*` constants), `:124-` (`registerLoopTools` body)
- Test: `internal/mcp/tools_loop_dj143_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_loop_dj143_test.go`:

```go
// DJ-143 — the three spec_loop_* tools are restricted to Codex and
// Gemini sessions. A Claude Code session calling any of them must
// receive a runtime-restriction error naming the runtime, the mode,
// the tool, and the docs reference. A Codex (or Gemini) session calls
// must pass through to the loop store unchanged.
package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecLoopBeginDeniesClaudeCodeSession(t *testing.T) {
	clearSessionRuntimes()
	server, cs := startServerWithClient(t, "claude-code", "interactive")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_begin",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "claude-code must be denied")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "spec_loop_begin")
	assert.Contains(t, tc.Text, "claude-code")
	assert.Contains(t, strings.ToLower(tc.Text), "interactive")
	assert.Contains(t, tc.Text, "docs/runtime-affordances.md")
	_ = server
}

func TestSpecLoopBeginAllowsCodexSession(t *testing.T) {
	clearSessionRuntimes()
	_, cs := startServerWithClient(t, "codex", "interactive")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_begin",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "codex must succeed")
}

func TestSpecLoopAllToolsDeniedForClaudeCode(t *testing.T) {
	for _, tool := range []string{"spec_loop_begin", "spec_loop_status", "spec_advance_iteration"} {
		t.Run(tool, func(t *testing.T) {
			clearSessionRuntimes()
			_, cs := startServerWithClient(t, "claude-code", "interactive")
			args := map[string]any{"activity": "spec_refinement", "target": "goals"}
			if tool == "spec_advance_iteration" {
				args["converged"] = false
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name: tool, Arguments: args,
			})
			require.NoError(t, err)
			assert.True(t, res.IsError, "%s must deny claude-code", tool)
		})
	}
}

// startServerWithClient wires a fresh NewSpecServer + ClientSession on
// in-memory transports, identifying the client with the supplied
// runtime + mode (mode forwarded via _meta["locutus.mode"]).
func startServerWithClient(t *testing.T, runtime, mode string) (*mcp.ServerSession, *mcp.ClientSession) {
	t.Helper()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)
	reg := activity.DefaultRegistry()
	server := NewSpecServer(store, nil, reg, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: runtime, Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), clientT, &mcp.ClientSessionOptions{
		InitializeParams: &mcp.InitializeParams{
			Meta: map[string]any{"locutus.mode": mode},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to fire and store the session context.
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if rt, _ := sessionRuntimeFor(ss); rt == runtime {
			return ss, cs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server never captured runtime=%q for session", runtime)
	return nil, nil
}
```

Add imports as needed: `"github.com/glorious-beard/locutus/internal/agent"`, `"github.com/glorious-beard/locutus/internal/specio"`. Check `agent.NewSpecStore` and `activity.DefaultRegistry` signatures exist; the wider test file `tools_loop_test.go` is a good cross-reference for how the package's existing tests construct these dependencies.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestSpecLoop' -v`
Expected: FAIL — all three tools currently allow any session; Claude Code calls succeed instead of erroring.

- [ ] **Step 3: Wrap the three registrations in `registerLoopTools`**

In `internal/mcp/tools_loop.go`, inside `registerLoopTools` (lines ~124-167), wrap each handler with `requireRuntimeAny(...)`. The current shape is:

```go
mcp.AddTool(server, &mcp.Tool{Name: "spec_loop_begin", Description: descSpecLoopBegin}, handler)
```

Becomes:

```go
mcp.AddTool(server, &mcp.Tool{Name: "spec_loop_begin", Description: descSpecLoopBegin},
	requireRuntimeAny(handler, "codex", "gemini"))
```

Apply the same wrapping to `spec_loop_status` and `spec_advance_iteration`. The handler functions stay literal closures or extract them to named functions if line length becomes awkward — match the surrounding style.

- [ ] **Step 4: Sharpen the three `desc*` constants**

In `internal/mcp/tools_loop.go` (around lines 24-32), prepend a leading sentence to each constant:

```go
descSpecLoopBegin = "For Codex or Gemini interactive self-loop only. Claude Code uses /goal to drive the loop and must not call this tool — calling from Claude Code returns a runtime-restriction error. " +
	"Begin (or recover) an interactive self-loop run. Pass activity (the activity you are executing, e.g. spec_refinement) and target …" // rest unchanged
```

Apply the identical leading sentence (substituting the tool's purpose-line) to `descSpecLoopStatus` and `descSpecAdvanceIteration`. The leading sentence is the same across all three; only the trailing original description differs.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run 'TestSpecLoop' -v`
Expected: PASS (claude-code denied with documented message; codex allowed; all three tools covered by the table-driven test).

Also run the wider mcp suite: `go test ./internal/mcp/ 2>&1 | tail -10`. Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_loop.go internal/mcp/tools_loop_dj143_test.go
git commit -m "feat(mcp): restrict spec_loop_* to codex/gemini via requireRuntime + sharpen descriptions (DJ-143)"
```

---

## Task 4: Bridge mode injection — parse-and-rewrite `initialize`

**Files:**
- Modify: `internal/mcp/bridge.go` (`BridgeIOToSocket` gains a `mode` parameter)
- Test: `internal/mcp/bridge_test.go` (create; check for an existing bridge_test.go first and extend if present)

- [ ] **Step 1: Write the failing test**

Create or extend `internal/mcp/bridge_test.go`:

```go
// DJ-143 — the bridge parses the client's first initialize JSON-RPC
// request, injects _meta["locutus.mode"] using the configured mode,
// and then drops to raw byte pumping for the rest of the session.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridgeInjectsLocutusModeIntoInitialize(t *testing.T) {
	// Mock server: listen on a tmp socket, accept one connection, read
	// one JSON-RPC line, decode it, capture _meta["locutus.mode"].
	sock := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		params, _ := msg["params"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		captured <- meta
	}()

	// Drive the bridge with an initialize request on its "stdin".
	in, inW := io.Pipe()
	out, outW := io.Pipe()
	defer in.Close()
	defer outW.Close()
	go func() {
		initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"claude-code","version":"test"}}}` + "\n"
		_, _ = inW.Write([]byte(initLine))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "headless")

	select {
	case meta := <-captured:
		require.NotNil(t, meta, "_meta must be present on initialize")
		assert.Equal(t, "headless", meta["locutus.mode"])
	case <-time.After(2 * time.Second):
		t.Fatal("server never received an initialize")
	}
}

func TestBridgePassesThroughNonInitializeLinesUnchanged(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		captured <- line
	}()

	in, inW := io.Pipe()
	out, outW := io.Pipe()
	defer in.Close()
	defer outW.Close()
	go func() {
		// A non-initialize first line (e.g. a notification) must pass
		// through verbatim. The bridge only rewrites the first
		// initialize it sees.
		nonInit := `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}` + "\n"
		_, _ = inW.Write([]byte(nonInit))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "headless")

	select {
	case got := <-captured:
		assert.Contains(t, string(got), "notifications/cancelled")
		assert.NotContains(t, string(got), "locutus.mode") // untouched
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the passthrough line")
	}
	_ = os.Remove(sock)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestBridgeInjects|TestBridgePassesThrough' -v`
Expected: FAIL — `BridgeIOToSocket` doesn't accept a `mode` arg; current code is `BridgeIOToSocket(ctx, sockPath, in, out)` (4 args). Test won't compile until the signature changes.

- [ ] **Step 3: Update `BridgeIOToSocket` signature + add the parse-and-inject phase**

In `internal/mcp/bridge.go`, update the function. Add a new helper for the JSON-line rewrite. The full new shape:

```go
func BridgeStdioToSocket(ctx context.Context, sockPath, mode string) error {
	return BridgeIOToSocket(ctx, sockPath, os.Stdin, os.Stdout, mode)
}

func BridgeIOToSocket(ctx context.Context, sockPath string, in io.Reader, out io.Writer, mode string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return fmt.Errorf("mcp: dial %s: %w", sockPath, err)
	}

	stopCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopCancel:
		}
	}()
	defer close(stopCancel)

	stdinDone := make(chan error, 1)
	go func() {
		err := pumpInWithInitInject(in, conn, mode)
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		stdinDone <- err
	}()

	_, copyErr := io.Copy(out, conn)
	_ = conn.Close()

	select {
	case <-stdinDone:
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
	}

	if copyErr != nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, net.ErrClosed) {
		return fmt.Errorf("mcp: bridge: %w", copyErr)
	}
	return nil
}

// pumpInWithInitInject scans incoming JSON-RPC lines on in, rewrites
// the first initialize request to include _meta["locutus.mode"]=mode,
// and then drops to raw io.Copy for the rest of the stream.
//
// Non-initialize lines that appear before the initialize are passed
// through verbatim (e.g. pre-init notifications). After the first
// initialize is forwarded, no further parsing occurs — only one
// initialize per MCP session.
func pumpInWithInitInject(in io.Reader, conn io.Writer, mode string) error {
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			rewritten, isInit := maybeInjectInitMode(line, mode)
			if _, werr := conn.Write(rewritten); werr != nil {
				return werr
			}
			if isInit {
				_, copyErr := io.Copy(conn, br)
				return copyErr
			}
		}
		if err != nil {
			return err
		}
	}
}

// maybeInjectInitMode tries to parse line as a JSON-RPC initialize
// request and inject params._meta["locutus.mode"]=mode. Returns the
// (possibly rewritten) line bytes and a boolean indicating whether an
// initialize was detected and rewritten. Non-JSON or non-initialize
// lines are returned unchanged with isInit=false.
func maybeInjectInitMode(line []byte, mode string) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return line, false
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return line, false
	}
	methodRaw, ok := msg["method"]
	if !ok {
		return line, false
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil || method != "initialize" {
		return line, false
	}
	var params map[string]any
	if rm, ok := msg["params"]; ok && len(rm) > 0 {
		_ = json.Unmarshal(rm, &params)
	}
	if params == nil {
		params = map[string]any{}
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["locutus.mode"] = mode
	params["_meta"] = meta
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return line, false
	}
	msg["params"] = paramsBytes
	out, err := json.Marshal(msg)
	if err != nil {
		return line, false
	}
	return append(out, '\n'), true
}
```

Add imports to `bridge.go`: `"bufio"`, `"encoding/json"`, `"strings"`. The existing imports list does not include these.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/mcp/ -run 'TestBridgeInjects|TestBridgePassesThrough' -v`
Expected: PASS.

Also verify the existing bridge code path (if any test exercises it via the old 4-arg signature) compiles. `go build ./...`; if the build fails, update the caller in `cmd/mcp.go` Task 5 first or temporarily call with `mode=""`.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/bridge.go internal/mcp/bridge_test.go
git commit -m "feat(mcp): bridge injects _meta[\"locutus.mode\"] into client's initialize (DJ-143)"
```

---

## Task 5: Bridge reads `LOCUTUS_MODE` env in `cmd/mcp.go`

**Files:**
- Modify: `cmd/mcp.go` (`McpCmd.Run`)
- Test: `cmd/mcp_test.go` *(extend or create)*

- [ ] **Step 1: Write the failing test**

The `McpCmd.Run` flow ends in a real socket dial (via `EnsureDaemon` + `BridgeStdioToSocket`), so unit-testing the env-var read in isolation is the highest-signal test. Create or extend `cmd/mcp_test.go`:

```go
// DJ-143 — McpCmd reads LOCUTUS_MODE env at startup, defaulting to
// "interactive" when unset. The resolved mode flows to BridgeStdioToSocket.
package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveLocutusModeUnsetDefaultsToInteractive(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "")
	assert.Equal(t, "interactive", resolveLocutusMode())
}

func TestResolveLocutusModeHeadless(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "headless")
	assert.Equal(t, "headless", resolveLocutusMode())
}

func TestResolveLocutusModeNormalizes(t *testing.T) {
	t.Setenv("LOCUTUS_MODE", "  Headless  ")
	assert.Equal(t, "headless", resolveLocutusMode())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestResolveLocutusMode -v`
Expected: FAIL — `resolveLocutusMode` undefined.

- [ ] **Step 3: Add the resolver and thread it through `McpCmd.Run`**

In `cmd/mcp.go`, add:

```go
// resolveLocutusMode reads LOCUTUS_MODE, normalizes it, defaults to
// "interactive" when absent. Per DJ-143 §2: headless ACP dispatch
// sets the env var on the spawned coding-agent process so its child
// `locutus mcp` bridge sees it; operator-typed sessions (interactive)
// leave it unset and inherit the default.
func resolveLocutusMode() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("LOCUTUS_MODE")))
	if v == "" {
		return "interactive"
	}
	return v
}
```

Add `"strings"` to the import block if not present.

Update `McpCmd.Run` to pass the resolved mode to `BridgeStdioToSocket`. The relevant line currently reads:

```go
	if err := mcp.BridgeStdioToSocket(ctx, sockPath); err != nil {
```

Becomes:

```go
	mode := resolveLocutusMode()
	if err := mcp.BridgeStdioToSocket(ctx, sockPath, mode); err != nil {
```

- [ ] **Step 4: Run tests + full build**

Run: `go test ./cmd/ -run TestResolveLocutusMode -v` (expect PASS).
Run: `go build ./...` to confirm no caller of `BridgeStdioToSocket` is left with a stale 2-arg call elsewhere.

- [ ] **Step 5: Commit**

```bash
git add cmd/mcp.go cmd/mcp_test.go
git commit -m "feat(cmd): bridge reads LOCUTUS_MODE env, defaults to interactive (DJ-143)"
```

---

## Task 6: ACP harness sets `LOCUTUS_MODE=headless` on the coding-agent spawn

**Files:**
- Modify: `internal/runner/run.go` (locate the `acp.AgentSpawns[runtime]` lookup, ~line 113; construct the headless-dispatch env there)
- Test: `internal/runner/run_dj143_test.go` (create)

- [ ] **Step 1: Inspect the current spawn-construction path**

Before writing the test, read `internal/runner/run.go:108-160` to see exactly how the `acp.Spawn` is currently consumed (e.g., is it passed directly to `acp.Open`, or copied first?). The plan assumes `spawn := acp.AgentSpawns[runtime]; spawn.Env = append(os.Environ(), "LOCUTUS_MODE=headless"); ...acp.Open(ctx, spawn, ...)`. If the existing code path passes the spawn straight from the map, the map entry must NOT be mutated (the map is package-global). Make a local copy first.

- [ ] **Step 2: Write the failing test**

Create `internal/runner/run_dj143_test.go`. The test exercises a small helper that the runner uses to compose the headless spawn env; pulling this out keeps the test focused without booting a full ACP child:

```go
// DJ-143 — the headless ACP-dispatch path sets LOCUTUS_MODE=headless on
// the spawned coding-agent's env so its child `locutus mcp` bridge can
// forward the mode to the daemon via _meta["locutus.mode"].
package runner

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/stretchr/testify/assert"
)

func TestHeadlessSpawnEnvIncludesLocutusMode(t *testing.T) {
	base := acp.Spawn{Cmd: "claude-agent-acp"}
	got := headlessSpawnEnv(base, []string{"PATH=/usr/bin", "HOME=/home/x"})

	// Original env passes through.
	assert.Contains(t, got.Env, "PATH=/usr/bin")
	assert.Contains(t, got.Env, "HOME=/home/x")
	// LOCUTUS_MODE=headless is appended.
	assert.Contains(t, got.Env, "LOCUTUS_MODE=headless")
	// Cmd is unchanged.
	assert.Equal(t, "claude-agent-acp", got.Cmd)
}

func TestHeadlessSpawnEnvOverridesPreExistingLocutusMode(t *testing.T) {
	base := acp.Spawn{Cmd: "claude-agent-acp"}
	// An inherited LOCUTUS_MODE=interactive would mislead the bridge;
	// the headless path must take precedence.
	got := headlessSpawnEnv(base, []string{"LOCUTUS_MODE=interactive", "PATH=/usr/bin"})

	// PATH still there.
	assert.Contains(t, got.Env, "PATH=/usr/bin")
	// LOCUTUS_MODE=headless wins.
	headlessCount := 0
	for _, e := range got.Env {
		if strings.HasPrefix(e, "LOCUTUS_MODE=") {
			assert.Equal(t, "LOCUTUS_MODE=headless", e)
			headlessCount++
		}
	}
	assert.Equal(t, 1, headlessCount, "exactly one LOCUTUS_MODE entry, value=headless")
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/runner/ -run TestHeadlessSpawnEnv -v`
Expected: FAIL — `headlessSpawnEnv` undefined.

- [ ] **Step 4: Add the helper and call it in the dispatch path**

In `internal/runner/run.go`, add the helper near the top of the file (after imports, before the dispatch function):

```go
// headlessSpawnEnv returns a copy of base with Env composed from
// parentEnv plus LOCUTUS_MODE=headless. Any pre-existing
// LOCUTUS_MODE entry in parentEnv is removed so the headless
// designation is unambiguous (an interactive shell that happened to
// export LOCUTUS_MODE=interactive must not override the ACP
// harness's intent).
//
// Per DJ-143 §2: the env var on the coding-agent process is
// inherited by its child `locutus mcp` bridge, which forwards it to
// the daemon via _meta["locutus.mode"].
func headlessSpawnEnv(base acp.Spawn, parentEnv []string) acp.Spawn {
	out := base
	env := make([]string, 0, len(parentEnv)+1)
	for _, e := range parentEnv {
		if strings.HasPrefix(e, "LOCUTUS_MODE=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "LOCUTUS_MODE=headless")
	out.Env = env
	return out
}
```

Add `"strings"` to imports if not present.

Then update the dispatch path that consumes `acp.AgentSpawns[runtime]`. Find the line at ~run.go:113 (`spawn, ok := acp.AgentSpawns[runtime]`) and replace the spawn-passing call with:

```go
	spawn, ok := acp.AgentSpawns[runtime]
	if !ok {
		// existing error handling
	}
	spawn = headlessSpawnEnv(spawn, os.Environ())
	// then pass `spawn` to whatever consumer (acp.Open / dispatch / etc.)
```

If the existing flow already mutates or copies the spawn somewhere, place the `headlessSpawnEnv` call so it happens once per dispatch and reaches `acp.Open`. Read the local code to land it correctly; the helper itself is the load-bearing piece.

Add `"os"` import to `internal/runner/run.go` if not already present.

- [ ] **Step 5: Run tests + full build**

Run: `go test ./internal/runner/ -run TestHeadlessSpawnEnv -v` (expect PASS).
Run: `go build ./...` (expect no errors).
Run: `go test ./internal/runner/ 2>&1 | tail -10` to confirm no existing runner test broke.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/run.go internal/runner/run_dj143_test.go
git commit -m "feat(runner): set LOCUTUS_MODE=headless on coding-agent spawn for headless dispatch (DJ-143)"
```

---

## Task 7: Docs — `runtime-affordances.md` Tool-Restriction passage + CLAUDE.md paragraph

**Files:**
- Modify: `docs/runtime-affordances.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add the Tool-Restriction passage to `docs/runtime-affordances.md`**

Locate the most appropriate section in `docs/runtime-affordances.md` (likely near the existing per-runtime / per-mode discussion of `/goal` and `spec_loop_*`). Add:

```markdown
## Tool-Restriction (DJ-143)

The Locutus MCP daemon registers tools globally per `Server.AddTool`,
but the MCP go-sdk does not expose per-session `tools/list` filtering.
Some tools — notably the DJ-142 `spec_loop_*` family — only make
sense for specific runtimes. DJ-143 enforces those scoping rules at
**call time** via a `requireRuntime` wrapper at each restricted
tool's registration site, paired with **list-time** signal in each
tool's `Description`.

Mechanism:

- Each session captures `(runtime, mode)` at MCP `initialize`:
  `clientInfo.name` → runtime; `_meta["locutus.mode"]` → mode.
- The mode field is set by the `locutus mcp` bridge from
  `LOCUTUS_MODE` env (default `interactive`); the ACP harness sets
  `LOCUTUS_MODE=headless` when it spawns the coding-agent runtime
  for dispatch.
- Restricted tools are wrapped with `requireRuntime(handler, "codex", "gemini")` at registration. A call from a denied runtime returns an MCP tool error of the shape:

  > `tool "spec_loop_begin" is not exposed to runtime "claude-code" (mode=interactive); allowed runtimes: codex, gemini; see docs/runtime-affordances.md § Tool-Restriction`

- Each restricted tool's `Description` starts with a leading sentence naming the runtime audience and contrasting against the wrong audience, so the agent reading the tool list at initialize time has a textual signal.

Operator note: there is **no hot-reload**. The runtime allowlist for a tool lives in code at the registration site; changes require a binary rebuild + daemon restart (`locutus mcp-stop` followed by the next connect re-forking the daemon).
```

- [ ] **Step 2: Add CLAUDE.md paragraph in Sources of Truth**

In `CLAUDE.md`, in the Sources of Truth section, after the existing DJ-142 paragraph (or DJ-141 paragraph if DJ-142's isn't there yet), add:

```markdown
- **Per-runtime tool restriction (DJ-143).** Some daemon tools are runtime-scoped — e.g. `spec_loop_*` is for Codex/Gemini interactive self-loop and not for Claude Code (which uses `/goal`). The MCP go-sdk's tool registry is server-global, so DJ-143 enforces the scoping at call time via a `requireRuntime` wrapper at each restricted tool's registration site, paired with a leading sentence in each tool's `Description` that names the runtime audience. Runtime comes from `ClientInfo.name` at MCP `initialize`; mode comes from `LOCUTUS_MODE` env var forwarded by the bridge as `_meta["locutus.mode"]`. A denied call returns an MCP error naming the runtime, mode, tool, and the docs reference. See [DJ-143](docs/decisions/dj-143-per-runtime-tool-policy.md) and `docs/runtime-affordances.md § Tool-Restriction`.
```

- [ ] **Step 3: Verify docs manifest bijection**

Run: `go test ./internal/docs/ -v`
Expected: PASS — DJ-143 file ↔ manifest row bijection holds (added in the design commit).

- [ ] **Step 4: Commit**

```bash
git add docs/runtime-affordances.md CLAUDE.md
git commit -m "docs(dj-143): tool-restriction passage + CLAUDE.md paragraph"
```

---

## Task 8: Full suite + end-to-end winplan validation

**Files:** none (validation only)

- [ ] **Step 1: Full suite + vet + race**

Run: `go build ./... && go vet ./... && go test ./... -race 2>&1 | tail -30`
Expected: all PASS.

- [ ] **Step 2: Rebuild binary**

```bash
go build -o locutus .
ls -la locutus
ls -la /Users/chetan/projects/winplan/locutus  # confirm symlink points at the new binary
```

- [ ] **Step 3: Clear the leaked daemon loop state from the diagnostic run**

In the winplan directory, drop the daemon socket so the next connect re-forks with a clean in-memory loop store:

```bash
( cd /Users/chetan/projects/winplan && ./locutus mcp-stop || true )
ls /Users/chetan/projects/winplan/.locutus/mcp.sock  # should be gone
```

- [ ] **Step 4: Interactive Claude Code validation**

Open winplan in Claude Code (VS Code interactive session). Run `/locutus-refine`. Expect:

- The agent's `tools/list` includes `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration`, BUT each description's leading sentence reads *"For Codex or Gemini interactive self-loop only. Claude Code uses /goal …"* — so the agent should not attempt to call them.
- If the agent does call one (e.g., misreading the description), the call result is an MCP error naming `claude-code`, `interactive`, the tool name, and `docs/runtime-affordances.md`.
- The `/goal` evaluator drives the loop across multiple iterations until convergence or the cap. No "Server iteration counter advanced to 1/20" prose appears in the agent's report.

Record the session: confirm in `.locutus/sessions/...` if a session dir was created (interactive sessions may not write here; the bridge's debug log shows the resolved `(runtime, mode)`).

- [ ] **Step 5: Headless validation against any active activity**

Run a headless dispatch in winplan to exercise the `LOCUTUS_MODE=headless` env path end-to-end:

```bash
( cd /Users/chetan/projects/winplan && ./locutus refine goals )
```

Watch the bridge's debug log (or whichever log the session writes) for the resolved `(runtime, mode) = (claude-code, headless)`. Confirm the run completes without runtime-restriction errors (headless `claude-code` is also denied `spec_loop_*` — the headless harness drives the loop, so the tools aren't called).

- [ ] **Step 6: Codex / Gemini interactive validation (if available)**

If a Codex or Gemini interactive session can attach to winplan's daemon, exercise the allow path:

```
# Inside a Codex/Gemini interactive session, invoke whatever surface
# triggers spec_loop_begin (e.g., /locutus-refine if it's published
# for the runtime). Expect the call to succeed and the agent to
# self-loop across iterations.
```

This is operator-decision validation; the test suite's integration test (Task 3) already exercises the allow path against an in-memory transport.

- [ ] **Step 7: Report**

Summarize: claude-code interactive saw the descriptions and (per design) didn't call `spec_loop_*`; `/goal` drove convergence; headless dispatch resolved as `(claude-code, headless)` via the env var; the leaked iteration-1 counter from the pre-DJ-143 diagnostic is cleared and can't recur.

---

## Self-Review

- **Spec coverage:** DJ-143 §1 (runtime ID at initialize) → Tasks 1, 2; §2 (mode via env + bridge + _meta) → Tasks 4, 5, 6; §3 (requireRuntime wrapper at registration) → Tasks 1, 3; §4 (sharpened descriptions) → Task 3; §5 (code surface) → all code tasks; §6 (operational cleanup) → Task 8 Step 3. All covered.
- **Placeholder scan:** No TBD/TODO; every code-changing step has the actual code; expected-output lines are present. Three judgment-required steps (Task 6 Step 1 reads the runner's actual spawn path; Task 7 Step 1 locates the right section in `runtime-affordances.md`; Task 8's interactive validation steps) explicitly tell the implementer to read first and adapt.
- **Type consistency:** `SessionRuntime` / `sessionRuntimeFor` / `storeSessionRuntime` / `requireRuntimeAny` names used identically across Tasks 1-3. The `mode string` parameter added to `BridgeIOToSocket` in Task 4 is consumed by Task 5's `McpCmd.Run`. The `headlessSpawnEnv` helper in Task 6 is consumed at the existing `acp.AgentSpawns[runtime]` lookup site.
- **Soft spots to resolve at execution time** (called out inline): Task 4 Step 4 assumes the bridge tests can compile without `cmd/mcp.go` being updated yet — if not, do Task 5 first or stub the caller temporarily. Task 6 Step 1 explicitly asks the implementer to inspect the live `run.go:108-160` to find the right insertion point before writing the test.
