package agent

import (
	"fmt"
	"path"
	"strings"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/specio"
)

// AgentDef is one agent definition loaded from a .md file. The
// frontmatter declares routing intent (Models priority list),
// structured-output schema, dispatch shape (MaxIterations), and the
// per-call timeout; the markdown body becomes SystemPrompt.
//
// Per-agent provider knobs (temperature, max_tokens, thinking
// budget) intentionally do NOT live on AgentDef. The executor
// applies tier-baked operational defaults from models.yaml so
// per-deployment tuning happens in one place rather than scattered
// across 25 agent files.
//
// There is no per-agent tool allowlist. Every tool registered in
// the Executor's ToolRegistry is exposed to every agent (resolved
// in buildAdapterRequest). The per-name allowlist was removed in
// the workflow-unification plan's Phase 4 because today's tools
// are all internal Locutus-defined read-only spec lookups, so the
// allowlist was documentation that loosely matched reality, not an
// enforcement surface. When external tools (with side effects)
// eventually land, a richer capability model will replace it.
type AgentDef struct {
	ID   string `yaml:"id"`
	Role string `yaml:"role"`
	// Models is the priority-ordered list of (provider, tier)
	// preferences this agent will accept. The executor walks the
	// list at dispatch time, picks the first preference whose
	// provider is configured, and resolves the tier through
	// models.yaml. An empty list is a config error — every agent
	// must declare at least one preference.
	Models []ModelPreference `yaml:"models"`
	// OutputSchema is the registry name of the strict-mode JSON
	// schema this agent's response must conform to (e.g.
	// "ScoutBrief", "RawSpecProposal"). Empty means free-form
	// output.
	OutputSchema string `yaml:"output_schema,omitempty"`
	// Grounding requests provider-native search grounding. Each
	// adapter attaches its provider's server-side search tool:
	// Gemini GoogleSearch, OpenAI web_search_preview, Anthropic
	// web_search_20250305. Retrieved sources land on the call's
	// Citations field (top-level and per-round) and propagate into
	// the session trace.
	Grounding bool `yaml:"grounding,omitempty"`
	// MaxIterations selects the dispatch shape. Zero (or one)
	// keeps the default single-call shape: one Generate, parse
	// output, return. Greater than one opts the agent into the
	// Locutus-side ReAct loop in AgentDispatcher.dispatchReAct —
	// each iteration calls the adapter, executes any tool_calls
	// the model emitted via the global ToolRegistry, appends the
	// results to the conversation, and loops until the model
	// emits a non-tool response or the cap is exceeded. Provider-
	// side grounding is independent of this field.
	MaxIterations int `yaml:"max_iterations,omitempty"`
	// Timeout caps per-call wall-clock duration as a Go duration
	// string ("5m", "30s"). Empty falls back to LOCUTUS_LLM_TIMEOUT
	// (default 15m). Tighten on fanout-bounded agents (per-node
	// elaborators) so a degenerate loop surfaces as a regular
	// cancellation rather than burning the global timeout.
	Timeout string `yaml:"timeout,omitempty"`
	// Thinking, when non-empty, overrides the tier-resolved
	// extended-thinking level for this agent's calls. Accepts the
	// same enum as models.yaml ("off" / "on" / "high"). The override
	// applies to every (provider, tier) pick this agent resolves to,
	// so an agent that wants thinking off on its balanced runs gets
	// it off on the openai/gemini balanced runs too.
	//
	// Use sparingly. The justify_synthesizer is the first caller:
	// claude-sonnet-4-6 was leaking thinking-block reasoning into a
	// constrained enum field (verdict), and the rationale field
	// already carries the synthesizer's reasoning, so thinking
	// budget was double-spent on output the validator then rejected.
	Thinking     string `yaml:"thinking,omitempty"`
	SystemPrompt string // markdown body, not from YAML
}

// LoadAgentDefs reads all .md files from the given directory on
// the FS and returns one AgentDef per file. Markdown body becomes
// SystemPrompt; frontmatter parses into the typed fields.
func LoadAgentDefs(fsys specio.FS, dir string) ([]AgentDef, error) {
	info, err := fsys.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("agent dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("agent dir %q: not a directory", dir)
	}

	paths, err := fsys.ListDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing agent dir %q: %w", dir, err)
	}

	var defs []AgentDef
	for _, p := range paths {
		if !strings.HasSuffix(path.Base(p), ".md") {
			continue
		}

		data, err := fsys.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading agent %q: %w", p, err)
		}

		var def AgentDef
		body, err := frontmatter.Parse(data, &def)
		if err != nil {
			return nil, fmt.Errorf("parsing agent %q: %w", p, err)
		}
		def.SystemPrompt = body

		defs = append(defs, def)
	}

	return defs, nil
}

// BuildSystemPrompt returns the agent's system prompt verbatim.
//
// DJ-130 follow-up: this used to append a `## Example output` block
// with the registered schema example as indented JSON. That dump
// reached the model in the system prompt on every thinking-off +
// schema call. Two problems:
//
//  1. JSON dumps in the system prompt prime JSON-mode output —
//     exactly the failure that DJ-130 surfaced (the model treats
//     the dump as "what shape my response should match" and emits
//     JSON throughout, dropping content between thinking and JSON
//     serialization).
//  2. The example bytes varied per-agent, so the system prompt was
//     per-agent, which broke cross-agent cache-prefix reuse on
//     providers that prefix-cache (OpenAI Responses, Gemini Pro).
//
// The example now reaches the model as a Cacheable user message
// (executor.buildAdapterRequest for single-call agents; adapter's
// runSplit for the split path). Same content surface; cleaner cache
// layering (system prompt stays byte-stable across calls of the
// same agent and even across agents whose system prompts are
// identical); and the example renders as prose via
// SchemaProsePromptDoc rather than as a JSON dump that primes
// JSON-mode output.
//
// BuildSystemPrompt remains the canonical entry point for "render
// the agent's system prompt for this call" so the session recorder
// and adapters route through one helper. The helper is now a thin
// pass-through; left in place for the lifecycle hook and for
// future extensions (e.g., per-agent guardrail prefixes).
func BuildSystemPrompt(def AgentDef) string {
	return def.SystemPrompt
}
