package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/glorious-beard/locutus/internal/runtimepolicy"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionRuntimes maps each per-connection MCP session key to its
// captured (runtime, mode) pair. The map is keyed by the session
// pointer (any) because the SDK does not expose user-attachable
// session metadata; storing alongside the SDK's session is the
// minimum-coupling alternative.
//
// Per DJ-143 §1: runtime comes from ClientInfo.name, mode from
// _meta["locutus.mode"] forwarded by the bridge.
//
// Known limitation: entries are never deleted. The go-sdk v1.6.1 does
// not expose a session-close hook, so there is no callback site for
// cleanup. The map grows by one entry per MCP session attach over the
// daemon's lifetime. For typical operator behavior (a handful of
// coding-agent sessions per day) the leak is negligible — each entry
// is two short strings. If the daemon starts being run as a long-
// lived service across many short sessions, add a periodic best-
// effort sweep or migrate to a custom transport that fires close
// callbacks. The sessionTokens map in tools_loop.go has the same
// shape and shares this constraint.
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
// allowlist.
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

// newInitializedHandler returns an InitializedHandler that, for each
// session whose initialize completes, reads ClientInfo.name (runtime)
// and _meta["locutus.mode"] (mode) from InitializeParams and stores
// them on the session map. Per DJ-143 §1+§2: runtime from the MCP
// protocol's clientInfo, mode from the bridge-forwarded _meta field
// (default "interactive" when absent).
//
// Per DJ-144 §9: also reads ClientInfo.version and logs a warning
// when the runtime is below its declared version floor. logger and
// fsys may be nil (nil logger = no-op; nil fsys = embedded defaults).
func newInitializedHandler(logger *slog.Logger, fsys specio.FS) func(context.Context, *mcp.InitializedRequest) {
	return func(_ context.Context, req *mcp.InitializedRequest) {
		if req == nil || req.Session == nil {
			return
		}
		params := req.Session.InitializeParams()
		if params == nil {
			return
		}
		runtime, version := "", ""
		if params.ClientInfo != nil {
			runtime = params.ClientInfo.Name
			version = params.ClientInfo.Version
		}
		mode := "interactive"
		if params.Meta != nil {
			if v, ok := params.Meta["locutus.mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = v
			}
		}
		storeSessionRuntime(req.Session, runtime, mode)
		checkRuntimeVersion(logger, fsys, runtime, version)
	}
}

// checkRuntimeVersion warns (never blocks) when a runtime connects
// below the minimum version Locutus relies on (DJ-144 §9). fsys is
// the project FS for the .borg/runtimes.yaml override (nil → embedded
// defaults). A nil logger is a no-op.
func checkRuntimeVersion(logger *slog.Logger, fsys specio.FS, runtime, version string) {
	if logger == nil {
		return
	}
	reg, err := runtimepolicy.NewRegistry(fsys)
	if err != nil {
		logger.Warn("runtimepolicy: registry load failed; skipping version check", "err", err)
		return
	}
	if msg, warn := reg.CheckVersion(runtime, version); warn {
		logger.Warn(msg)
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
