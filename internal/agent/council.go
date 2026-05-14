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

// BuildSystemPrompt returns the agent's system prompt with an
// optional JSON example payload appended.
//
// The example is appended only when BOTH conditions hold:
//
//  1. The agent declares an OutputSchema. (Without it, there's
//     nothing to demonstrate.)
//  2. The agent's thinking mode is off. Thinking-on agents that
//     emit structured output run through the dispatcher's two-call
//     split (reasoning call has no schema; format call extracts
//     into JSON), so a JSON example on the reasoning call would
//     suggest a JSON output shape the reasoning call isn't asked
//     to produce.
//
// Schema descriptions reach the model via the provider's strict-
// mode structured-output configuration on every call. The example
// payload here is complementary — concrete shape demonstration
// alongside the description-driven field semantics. Agents that
// want positive "cover these aspects" prose framing add it to
// their .md directly.
func BuildSystemPrompt(def AgentDef) string {
	if def.OutputSchema == "" {
		return def.SystemPrompt
	}
	if def.Thinking != "" && def.Thinking != "off" {
		return def.SystemPrompt
	}
	doc := SchemaPromptDoc(def.OutputSchema)
	if doc == "" {
		return def.SystemPrompt
	}
	return def.SystemPrompt + "\n\n## Example output\n\n```json\n" + doc + "\n```\n"
}
