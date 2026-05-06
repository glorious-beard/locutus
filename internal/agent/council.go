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
// structured-output schema, tools, and the per-call timeout; the
// markdown body becomes SystemPrompt.
//
// Per-agent provider knobs (temperature, max_tokens, thinking
// budget) intentionally do NOT live on AgentDef. The executor
// applies tier-baked operational defaults from models.yaml so
// per-deployment tuning happens in one place rather than scattered
// across 25 agent files.
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
	// Grounding requests provider-native search grounding when
	// the resolved adapter supports it (Gemini GoogleSearch,
	// OpenAI web_search_preview). Anthropic falls back to
	// ungrounded with a Warn — the SDK doesn't expose web_search
	// today.
	Grounding bool `yaml:"grounding,omitempty"`
	// Tools names tools this agent may invoke. Each must be
	// registered in the Executor's ToolRegistry; resolution
	// happens at dispatch time. Tools and grounding are mutually
	// exclusive on Gemini — the adapter logs a Warn and falls back
	// to schema-doc-only output enforcement when both are set.
	Tools []string `yaml:"tools,omitempty"`
	// Timeout caps per-call wall-clock duration as a Go duration
	// string ("5m", "30s"). Empty falls back to LOCUTUS_LLM_TIMEOUT
	// (default 15m). Tighten on fanout-bounded agents (per-node
	// elaborators) so a degenerate loop surfaces as a regular
	// cancellation rather than burning the global timeout.
	Timeout      string `yaml:"timeout,omitempty"`
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

// BuildSystemPrompt returns the agent's system prompt with the
// output-schema documentation appended when the agent declares one.
// The schema doc is the indented JSON of the registered example
// struct — providers with strict-mode coverage get it as the API
// contract via the executor; the prompt copy gives the model the
// same shape as a textual reference, which materially reduces
// drift when free-form-text fields appear inside a structured
// output.
func BuildSystemPrompt(def AgentDef) string {
	prompt := def.SystemPrompt
	if def.OutputSchema == "" {
		return prompt
	}
	doc := SchemaPromptDoc(def.OutputSchema)
	if doc == "" {
		return prompt
	}
	return prompt + "\n\n## Output JSON Schema\n\n```json\n" + doc + "\n```\n"
}
