package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/chetan/locutus/internal/specio"
)

// getLLM returns an AgentExecutor backed by per-provider direct-SDK
// adapters for commands that need it. Returns a typed error when no
// provider is configured so callers can render a friendly message.
//
// Most subcommands should use recordingLLM instead — getLLM is the
// raw inner executor with no session transcript. Tests and dispatch
// (which has its own logging) are the legitimate raw consumers.
func getLLM() (agent.AgentExecutor, error) {
	if !agent.LLMAvailable() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s, %s, or %s",
			agent.EnvKeyAnthropicAPI, agent.EnvKeyGeminiAPI, agent.EnvKeyOpenAIAPI,
		)
	}
	exec, err := buildExecutor()
	if err != nil {
		return nil, err
	}
	emitBannerOnce(exec)
	return exec, nil
}

// recordingLLM wraps getLLM with a SessionRecorder so every council
// / spec-generation agent call is captured to .locutus/sessions/<sid>/
// for after-the-fact inspection. command is recorded in the file
// metadata for human reference (e.g. "refine goals", "import
// docs/x.md").
//
// The heartbeat is off by default — the caller flips it on via
// recordingLLMForMode when there's no per-call UI to take its place.
//
// Spec-lookup tools (spec_list_manifest / spec_get) are registered
// against the executor's tool registry so the spec-reconciler agent
// can navigate the persisted spec lazily instead of inlining the
// full ExistingSpec into its prompt. Registration is bound to fsys
// so tool calls read from the same filesystem the rest of the
// command operates on.
//
// Returns the wrapped executor, the recorder (so callers can log
// the session path to stdout), and any error from constructing
// either.
func recordingLLM(fsys specio.FS, root, command string) (agent.AgentExecutor, *agent.SessionRecorder, error) {
	inner, err := getLLM()
	if err != nil {
		return nil, nil, err
	}
	if err := registerSpecToolsOnce(inner, fsys, root); err != nil {
		return nil, nil, fmt.Errorf("register spec tools: %w", err)
	}
	rec, err := agent.NewSessionRecorder(fsys, command, root)
	if err != nil {
		return nil, nil, err
	}
	initTracerForSession(rec, root)
	return agent.NewLoggingExecutorWithHeartbeat(inner, rec, heartbeatEnabledForMode()), rec, nil
}

// tracerShutdownOnce gates a single OTel TracerProvider per process.
// recordingLLM may be called multiple times in one CLI invocation
// (each subcommand path constructs its own recorder); only the first
// initializes the SDK. The shutdown registered with atexit / defer
// at the cmd layer is captured here so re-init calls can no-op
// safely.
var (
	tracerInitOnce  sync.Once
	tracerShutdown  func()
	tracerInitError error
)

// initTracerForSession wires the OTel TracerProvider once per process,
// pointing at the first session's directory for the OTLP-JSON file
// artifact. Subsequent recordingLLM calls in the same process inherit
// the same provider — every session's spans land in the first
// session's trace.jsonl. That's a known limitation of doing
// process-wide TracerProvider setup from a per-call helper; in
// practice each `locutus <verb>` invocation is one process and one
// session, so the limitation is theoretical for CLI use. MCP servers
// run multiple sessions per process and would benefit from
// per-session providers — that's a follow-up if MCP traces become a
// priority.
//
// Failures here log and proceed: the file exporter's failure mode is
// "no trace file written"; the rest of the recording path
// (per-call YAMLs, session manifest) remains the always-on artifact.
func initTracerForSession(rec *agent.SessionRecorder, root string) {
	tracerInitOnce.Do(func() {
		absSessionDir := filepath.Join(root, rec.Path())
		shutdown, err := agent.InitTracer(absSessionDir)
		if err != nil {
			tracerInitError = err
			slog.Warn("otel: tracer init failed; per-call YAMLs remain the trace surface",
				"session_dir", absSessionDir, "error", err)
			return
		}
		tracerShutdown = shutdown
	})
}

// ShutdownTracer flushes the OTel TracerProvider if one was
// initialized. Safe to call from main / CLI exit handlers; no-op when
// the SDK was never initialized (read-only verbs that bypass
// recordingLLM never construct a provider).
func ShutdownTracer() {
	if tracerShutdown != nil {
		tracerShutdown()
	}
}

// executorOnce caches the process-wide Executor. Constructing the
// per-provider adapter set involves SDK client initialisation and
// model-config parsing, both of which we want to do exactly once.
var (
	executorOnce sync.Once
	sharedExec   *agent.Executor
	executorErr  error
)

func buildExecutor() (*agent.Executor, error) {
	executorOnce.Do(func() {
		sharedExec, executorErr = newExecutor()
	})
	return sharedExec, executorErr
}

func newExecutor() (*agent.Executor, error) {
	cfg, err := agent.LoadModelConfig()
	if err != nil {
		return nil, fmt.Errorf("load model config: %w", err)
	}
	providers := agent.DetectProviders()

	var adapterSet []adapters.Adapter
	if providers.Anthropic {
		a, err := adapters.NewAnthropicAdapter()
		if err != nil {
			return nil, fmt.Errorf("anthropic adapter: %w", err)
		}
		adapterSet = append(adapterSet, a)
	}
	if providers.GoogleAI {
		a, err := adapters.NewGeminiAdapter(context.Background())
		if err != nil {
			return nil, fmt.Errorf("gemini adapter: %w", err)
		}
		adapterSet = append(adapterSet, a)
	}
	if providers.OpenAI {
		a, err := adapters.NewOpenAIResponsesAdapter()
		if err != nil {
			return nil, fmt.Errorf("openai adapter: %w", err)
		}
		adapterSet = append(adapterSet, a)
	}
	return agent.NewExecutor(cfg, providers, adapterSet, nil)
}

// specToolsOnce gates spec-tool registration so multiple
// recordingLLM callers share a single registration against the
// process-wide executor.
//
// specToolsErr captures any error from search.Open inside the
// sync.Once body so subsequent recordingLLM calls in the same
// process see the same failure rather than racing past a missing
// index. The error path is propagated up through registerSpecToolsOnce
// so the CLI subcommand can surface a clean message instead of every
// spec_search call failing later with an opaque "open index" error.
var (
	specToolsOnce sync.Once
	specToolsErr  error
)

func registerSpecToolsOnce(inner agent.AgentExecutor, fsys specio.FS, projectRoot string) error {
	exec, ok := inner.(*agent.Executor)
	if !ok {
		// Mock executors in tests don't have a tool registry; nothing
		// to register against. Agents that reference tools by name
		// will simply not have the dispatch path engaged — the
		// mocked response returns the verdict directly.
		return nil
	}
	specToolsOnce.Do(func() {
		// DJ-134: one in-process spec store backs every RAG tool
		// (spec_list_manifest, spec_get, spec_search) AND the council's
		// in-flight reads/writes. NewSpecStore loads .borg/spec/ once
		// at startup, opens the in-memory Bluge index, and serves all
		// three tools through one mutex-protected surface. The store
		// is registered with the executor (council reaches it via
		// SpecStore() through the wrapper chain) and with the tool
		// registry (RegisterSpecTools binds handlers to it).
		store, err := agent.NewSpecStore(fsys)
		if err != nil {
			specToolsErr = fmt.Errorf("open spec store: %w", err)
			return
		}
		exec.SetSpecStore(store)
		agent.RegisterSpecTools(exec.Tools(), store)
	})
	return specToolsErr
}

// heartbeatEnabledForMode reports whether the LoggingExecutor
// heartbeat should fire in the current render mode. The heartbeat
// exists to reassure operators that a long-running call is still
// alive; it is redundant in modes that already render per-call
// progress.
//
// Rules:
//   - Rich CLI: spinner shows elapsed time per agent → off.
//   - Silent (--json): caller wants quiet stderr → off.
//   - Plain CLI: structured logs are the only progress signal → on.
//   - MCP: protocol notifications are agent-level, not per-LLM-call;
//     the heartbeat fills the gap when one call within an agent
//     hangs. stderr in an MCP server is generally captured but not
//     forwarded to the client, so this is a debugging aid for the
//     operator running the server, not a protocol leak.
func heartbeatEnabledForMode() bool {
	if globalCLI == nil {
		return true
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

// emitBannerOnce writes a one-line banner to stderr. Only fires once
// per process. Suppressed when the program is serving MCP (the
// banner would corrupt protocol clients that capture stderr) or
// when --json output is requested (silent mode wants a clean run).
//
// Banner goes to stderr because it's operational metadata, not the
// command's result; this lets `locutus refine | grep ...` pipe
// stdout without the banner interleaving.
func emitBannerOnce(exec *agent.Executor) {
	bannerOnce.Do(func() {
		if globalCLI == nil {
			fmt.Fprintln(os.Stderr, exec.Banner())
			return
		}
		switch globalCLI.RenderMode() {
		case RenderModeRich, RenderModePlain:
			fmt.Fprintln(os.Stderr, exec.Banner())
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
