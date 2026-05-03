package cmd

import (
	"fmt"
	"os"
	"sync"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// getLLM returns a Genkit-backed LLM for commands that need it. Returns a
// typed error when no provider is configured so callers can render a friendly
// message; production commands should guard against this case and point the
// user at the env vars Genkit reads.
//
// Most subcommands should use recordingLLM instead — getLLM is the raw
// inner LLM with no session transcript. Tests and dispatch (which has its
// own logging) are the legitimate raw consumers.
func getLLM() (agent.LLM, error) {
	if !agent.LLMAvailable() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s, %s, or %s",
			agent.EnvKeyAnthropicAPI, agent.EnvKeyGeminiAPI, agent.EnvKeyGoogleAPI,
		)
	}
	llm, err := agent.NewGenKitLLM()
	if err != nil {
		return nil, err
	}
	emitBannerOnce(llm)
	return llm, nil
}

// recordingLLM wraps getLLM with a SessionRecorder so every council /
// spec-generation LLM call is captured to .locutus/sessions/<sid>.yaml
// for after-the-fact inspection. command is recorded in the file
// metadata for human reference (e.g. "refine goals", "import docs/x.md").
//
// The heartbeat is off by default — the caller flips it on via
// recordingLLMForMode when there's no per-call UI to take its place.
//
// Spec-lookup tools (spec_list_manifest / spec_get) are registered
// against the underlying Genkit runtime so the spec_reconciler agent
// can navigate the persisted spec lazily instead of inlining the full
// ExistingSpec into its prompt. Registration is bound to fsys so tool
// calls read from the same filesystem the rest of the command operates
// on (OSFS in production; MemFS in tests via the same path).
//
// Returns the wrapped LLM, the recorder (so callers can log the session
// path to stdout), and any error from constructing either.
func recordingLLM(fsys specio.FS, root, command string) (agent.LLM, *agent.SessionRecorder, error) {
	inner, err := getLLM()
	if err != nil {
		return nil, nil, err
	}
	registerSpecToolsOnce(inner, fsys)
	rec, err := agent.NewSessionRecorder(fsys, command, root)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewLoggingLLMWithHeartbeat(inner, rec, heartbeatEnabledForMode()), rec, nil
}

// specToolsOnce gates Genkit's DefineTool against double registration —
// the runtime panics on duplicate names, and the shared GenKitLLM
// instance lives for the process lifetime. Tied to the inner LLM so a
// future per-LLM scope (e.g. tests that build their own GenKitLLM)
// would re-register cleanly.
var specToolsOnce sync.Once

func registerSpecToolsOnce(inner agent.LLM, fsys specio.FS) {
	gk, ok := inner.(*agent.GenKitLLM)
	if !ok {
		// Mock LLMs in tests don't have a Genkit runtime; nothing to
		// register against. The agents that reference tools by name
		// will simply not have the tool dispatch path engaged — the
		// mocked response returns the verdict directly.
		return
	}
	specToolsOnce.Do(func() {
		agent.RegisterSpecTools(gk.Genkit(), fsys)
	})
}

// heartbeatEnabledForMode reports whether the LoggingLLM heartbeat
// should fire in the current render mode. The heartbeat exists to
// reassure operators that a long-running call is still alive; it is
// redundant in modes that already render per-call progress.
//
// Rules:
//   - Rich CLI: spinner shows elapsed time per agent → off.
//   - Silent (--json): caller wants quiet stderr → off.
//   - Plain CLI: structured logs are the only progress signal → on.
//   - MCP: protocol notifications are agent-level, not LLM-call-level;
//     the heartbeat fills the gap when one call within an agent hangs.
//     stderr in an MCP server is generally captured but not forwarded
//     to the client, so this is a debugging aid for the operator
//     running the server, not a protocol leak.
func heartbeatEnabledForMode() bool {
	if globalCLI == nil {
		return true // default-on for tests / unparsed contexts
	}
	switch globalCLI.RenderMode() {
	case RenderModePlain, RenderModeMCP:
		return true
	default:
		return false
	}
}

// bannerOnce ensures the model banner is printed at most once per
// process — multiple subcommands or council passes in one run share
// the same line.
var bannerOnce sync.Once

// emitBannerOnce writes a one-line model banner to stderr. Only fires
// once per process. Suppressed when the program is serving MCP (the
// banner would corrupt protocol clients that capture stderr) or when
// --json output is requested (silent mode wants a clean run).
//
// Banner goes to stderr because it's operational metadata, not the
// command's result; this lets `locutus refine | grep ...` pipe stdout
// without the banner interleaving.
func emitBannerOnce(llm *agent.GenKitLLM) {
	bannerOnce.Do(func() {
		if globalCLI == nil {
			fmt.Fprintln(os.Stderr, llm.Banner())
			return
		}
		switch globalCLI.RenderMode() {
		case RenderModeRich, RenderModePlain:
			fmt.Fprintln(os.Stderr, llm.Banner())
		}
	})
}

// globalCLI is set by the kong-bound CLI struct once it's parsed so
// helpers in this package (which run inside subcommand methods) can
// reach it without threading it through every signature. It is
// populated from a CLI's AfterApply / Run path; tests that don't go
// through kong leave it nil and emitBannerOnce falls back to default
// behavior (always print).
var globalCLI *CLI
