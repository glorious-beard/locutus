## DJ-066: Genkit Wired with Env-Driven Plugin Auto-Detection (Completes DJ-003)

**Status:** shipped

**Decision:** `internal/agent/genkit.go` no longer stubs. `NewGenKitLLM()` inspects the environment via `DetectProviders()`, registers `github.com/firebase/genkit/go/plugins/anthropic` when `ANTHROPIC_API_KEY` is present and `github.com/firebase/genkit/go/plugins/googlegenai` when `GEMINI_API_KEY` or `GOOGLE_API_KEY` is present, and exposes an `agent.LLM` backed by `genkit.GenerateText`.

**Completes DJ-003.** The original decision committed to Genkit Go as the LLM abstraction layer but left `Generate()` returning `"GenKit LLM provider not yet wired"`. That stub is replaced; the live smoke test (`TestGenKitLLM_LiveSmoke` with `LOCUTUS_INTEGRATION_TEST=1`) hits a real provider through a real API key loaded from `.env`.

**Why env-driven auto-detection:** the Genkit plugins `panic` during `Init` if their API-key env var is missing. Registering a plugin unconditionally would brick the whole process for any user who hasn't set up *every* provider. `DetectProviders()` inspects env first, registers only the matching plugins — a Gemini-only user never pulls in the Anthropic plugin (and vice versa). `sync.Once` guards `genkit.Init` against the plugins' second-initialization panic.

**Claude Max subscription caveat:** Anthropic's OAuth (used by the `claude` CLI, zero-cost for Max subscribers) can't be used by the Go SDK — the Anthropic Go SDK requires an API key. So a user with Claude Max who sets `ANTHROPIC_API_KEY` burns API tokens, not Max credits. This is surfaced in the Genkit wire-up commit's rationale and mentioned when users face the choice.

**Provider prefix required:** Genkit requires `anthropic/...` or `googleai/...` on every model string — it routes by prefix. Model strings without a prefix fall back to the configured default via `GenKitLLM.resolveModel`. Callers can override at three layers: `LOCUTUS_MODEL` env var (global), per-`AgentDef.Model` field (per agent), or `LOCUTUS_MODELS_CONFIG` YAML override (per-project or per-user — see DJ-067).
