package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/runtimepolicy"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionRuntimes maps each per-connection MCP session key to its
// captured (runtime, mode, dryRun, dryRunFormat) tuple. The map is
// keyed by the session pointer (any) because the SDK does not expose
// user-attachable session metadata; storing alongside the SDK's
// session is the minimum-coupling alternative.
//
// Per DJ-143 §1: runtime comes from ClientInfo.name, mode from
// _meta["locutus.mode"] forwarded by the bridge.
//
// Per DJ-147 §2: dryRun + dryRunFormat come from
// _meta["locutus.dry_run"] + _meta["locutus.dry_run_format"] forwarded
// by the bridge when the operator passed --dry-run on the CLI. The
// InitializedHandler also calls SpecStore.RegisterOverlay for dry-run
// sessions so subsequent spec_* writes are captured rather than
// persisted.
//
// Known limitation: entries are never deleted. The go-sdk v1.6.1 does
// not expose a session-close hook, so there is no callback site for
// cleanup. The map grows by one entry per MCP session attach over the
// daemon's lifetime. For typical operator behavior (a handful of
// coding-agent sessions per day) the leak is negligible — each entry
// is two short strings plus two scalars. The SpecStore overlay map
// shares this constraint: an overlay registered for a dry-run session
// persists until the daemon restarts. If the daemon starts being run
// as a long-lived service across many short sessions, add a periodic
// best-effort sweep or migrate to a custom transport that fires close
// callbacks. The sessionTokens map in tools_loop.go has the same
// shape and shares this constraint.
var (
	sessionRuntimesMu sync.RWMutex
	sessionRuntimes   = map[any]sessionContext{}
)

type sessionContext struct {
	runtime      string
	mode         string
	dryRun       bool
	dryRunFormat string
}

// storeSessionContext stores the (runtime, mode, dryRun, dryRunFormat)
// tuple for a session. Called from the InitializedHandler at session
// start and from tests to override.
func storeSessionContext(sess any, runtime, mode string, dryRun bool, dryRunFormat string) {
	sessionRuntimesMu.Lock()
	defer sessionRuntimesMu.Unlock()
	sessionRuntimes[sess] = sessionContext{
		runtime:      strings.ToLower(strings.TrimSpace(runtime)),
		mode:         strings.ToLower(strings.TrimSpace(mode)),
		dryRun:       dryRun,
		dryRunFormat: strings.ToLower(strings.TrimSpace(dryRunFormat)),
	}
}

// storeSessionRuntime preserves the DJ-143 entry point (runtime + mode
// only) for tests that don't care about dry-run. New callers should
// prefer storeSessionContext.
func storeSessionRuntime(sess any, runtime, mode string) {
	storeSessionContext(sess, runtime, mode, false, "")
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

// sessionDryRunFor is the internal any-keyed accessor mirroring
// sessionRuntimeFor; the public *mcp.ServerSession accessors below
// are thin wrappers so tests can use &fakeSessionKey{} as a sentinel.
func sessionDryRunFor(sess any) bool {
	sessionRuntimesMu.RLock()
	defer sessionRuntimesMu.RUnlock()
	c, ok := sessionRuntimes[sess]
	if !ok {
		return false
	}
	return c.dryRun
}

func sessionDryRunFormatFor(sess any) string {
	sessionRuntimesMu.RLock()
	defer sessionRuntimesMu.RUnlock()
	c, ok := sessionRuntimes[sess]
	if !ok {
		return ""
	}
	return c.dryRunFormat
}

// SessionDryRun reports whether the calling session is in dry-run
// mode (per DJ-147 §2 the captureOnly wrapper consults this).
// Returns false for unknown sessions — the safe default; the wrapper
// passes through to the inner handler.
func SessionDryRun(sess *mcp.ServerSession) bool {
	return sessionDryRunFor(sess)
}

// SessionDryRunFormat returns the format the operator requested via
// --format (markdown or json). Empty for sessions not in dry-run.
func SessionDryRunFormat(sess *mcp.ServerSession) string {
	return sessionDryRunFormatFor(sess)
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

// captureOnly wraps a tool handler so that dry-run sessions land their
// would-be input in the overlay (via the caller-supplied capture
// function) rather than running the inner handler.
//
// Per DJ-147 §4: composes with requireRuntimeAny — apply requireRuntime
// outermost so denied runtimes fail before capture runs.
//
// The capture function receives the same In the inner handler would
// have received; it's expected to validate input identically (so the
// agent gets the same shape-error feedback in both modes) and to call
// store.OverlayPut / OverlayDelete via a closure over the SpecStore.
func captureOnly[In, Out any](
	h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	capture func(sess *mcp.ServerSession, in In) (Out, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if !SessionDryRun(req.Session) {
			return h(ctx, req, in)
		}
		out, err := capture(req.Session, in)
		if err != nil {
			var zero Out
			return errorResult(err.Error()), zero, nil
		}
		return textResult("captured (dry-run)"), out, nil
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
// when the runtime is below its declared version floor.
//
// Per DJ-147 §2: also reads _meta["locutus.dry_run"] (bool or
// "true"/"1") and _meta["locutus.dry_run_format"] (markdown|json,
// default markdown). When dry-run is set and a non-nil store is
// supplied, calls store.RegisterOverlay so subsequent spec_* writes
// route to the per-session overlay instead of persisting.
//
// logger, fsys, and store may each be nil — nil logger silences the
// version-warning log; nil fsys falls back to the embedded
// runtime-policy defaults; nil store skips overlay registration for
// dry-run sessions (useful in unit tests that don't exercise the
// overlay surface).
func newInitializedHandler(logger *slog.Logger, fsys specio.FS, store *agent.SpecStore) func(context.Context, *mcp.InitializedRequest) {
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
		dryRun := false
		dryRunFormat := "markdown"
		if params.Meta != nil {
			if v, ok := params.Meta["locutus.mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = v
			}
			if v, ok := params.Meta["locutus.dry_run"]; ok {
				// Accept bool, "1", "true" (case-insensitive). Everything else → false.
				switch t := v.(type) {
				case bool:
					dryRun = t
				case string:
					s := strings.ToLower(strings.TrimSpace(t))
					dryRun = s == "1" || s == "true"
				}
			}
			if v, ok := params.Meta["locutus.dry_run_format"].(string); ok {
				s := strings.ToLower(strings.TrimSpace(v))
				if s == "json" || s == "markdown" {
					dryRunFormat = s
				}
			}
		}
		storeSessionContext(req.Session, runtime, mode, dryRun, dryRunFormat)
		checkRuntimeVersion(logger, fsys, runtime, version)
		if dryRun && store != nil {
			store.RegisterOverlay(req.Session)
		}
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
