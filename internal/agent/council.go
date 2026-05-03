package agent

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/specio"
)

// CapabilityTier represents the model capability level an agent requires.
type CapabilityTier string

const (
	CapabilityFast     CapabilityTier = "fast"     // cheap, fast — convergence, historian, classification
	CapabilityBalanced CapabilityTier = "balanced"  // default — planning, critique, analysis
	CapabilityStrong   CapabilityTier = "strong"    // expensive, powerful — complex architecture, stuck agents
)

// DefaultModel is the last-resort fallback used when no capability
// tier is specified on an agent def AND the tier→provider resolver
// (ModelConfig.ResolveTier) can't find a match. Kept as a constant so
// callers that hardcode a default (notably triage.go) have a stable
// symbol; the preferred path is to specify a Capability tier on the
// agent def and let the config file decide.
const DefaultModel = "anthropic/claude-sonnet-4-6"

// AgentDef is an agent definition loaded from a .md file.
//
// MaxTokens and ThinkingBudget are per-agent overrides for the
// equivalent fields on GenerateRequest. Their precedence at request-
// build time:
//
//   - MaxTokens: agent value > models.yaml per-model knob > provider
//     fallback. Set on agents whose output is bounded (e.g. critics
//     that emit a few short issues) to keep responses tight without
//     editing call sites.
//   - ThinkingBudget: agent value > 0 enables provider-side extended
//     thinking with that many reasoning tokens; 0 leaves it off.
//     Worth turning on for judgment-heavy agents (scout, architect,
//     reconciler, critics); waste of tokens on mechanical agents
//     (rewriter, synthesizer).
type AgentDef struct {
	ID             string         `yaml:"id"`
	Role           string         `yaml:"role"`
	Model          string         `yaml:"model,omitempty"`
	Capability     CapabilityTier `yaml:"capability,omitempty"`
	Temperature    float64        `yaml:"temperature,omitempty"`
	MaxTokens      int            `yaml:"max_tokens,omitempty"`
	ThinkingBudget int            `yaml:"thinking_budget,omitempty"`
	OutputSchema   string         `yaml:"output_schema,omitempty"` // type name for JSON schema injection
	// Grounding, when true, enables provider-native search-grounding
	// for this agent's calls (Gemini's GoogleSearch tool today;
	// Anthropic web_search when Genkit Go's plugin exposes it).
	// Worth turning on for survey/research agents whose value depends
	// on current state of practice; pure judgment agents leave it off.
	Grounding bool `yaml:"grounding,omitempty"`
	// Tools names Genkit-registered tools this agent may call during
	// generation. The runtime threads them into the request's
	// ai.WithTools option. Currently used by spec_reconciler to
	// navigate the persisted spec via spec_list_manifest / spec_get
	// instead of receiving the entire ExistingSpec inlined into its
	// prompt. Tools and grounding are mutually exclusive on Gemini;
	// frontmatter that combines both will silently lose grounding
	// when the model rejects the combination at API time.
	Tools        []string `yaml:"tools,omitempty"`
	SystemPrompt string   // the markdown body (not from YAML)
}

// LoadAgentDefs reads all .md files from the given directory on the FS.
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

// BuildGenerateRequest constructs a GenerateRequest from an AgentDef and
// user messages. The system prompt is prepended as a system-role message.
// If the agent has a Capability tier, the model is resolved from DefaultModels.
// If the agent has an OutputSchema, the JSON schema is appended to the system prompt.
func BuildGenerateRequest(def AgentDef, messages []Message) GenerateRequest {
	model := resolveModel(def)

	systemPrompt := def.SystemPrompt

	// If the agent declares an output_schema, look up the registered
	// example value. We pass it through GenerateRequest.OutputSchema so
	// the LLM provider enforces JSON output natively (Anthropic forced
	// tool-use, Gemini responseSchema). Also append the schema example
	// to the system prompt as documentation for providers that don't
	// support native constrained output.
	var schemaValue any
	if def.OutputSchema != "" {
		if schema, ok := schemaRegistry[def.OutputSchema]; ok {
			schemaValue = schema
			schemaJSON, err := json.MarshalIndent(schema, "", "  ")
			if err == nil {
				systemPrompt += "\n\n## Output JSON Schema\n\n```json\n" + string(schemaJSON) + "\n```\n"
			}
		}
	}

	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, messages...)

	return GenerateRequest{
		Model:          model,
		Messages:       msgs,
		Temperature:    def.Temperature,
		MaxTokens:      def.MaxTokens,
		ThinkingBudget: def.ThinkingBudget,
		OutputSchema:   schemaValue,
		Grounding:      def.Grounding,
		Tools:          def.Tools,
	}
}

// resolveModel determines the model string for an agent.
// Priority: explicit Model field > Capability tier resolved against
// available providers (via ModelConfig + DetectProviders) > DefaultModel.
//
// Resolving via ModelConfig means the tier→model pick is runtime-aware:
// a Gemini-only user gets a googleai/ model even when the balanced tier
// lists an anthropic/ entry first, as long as a googleai/ entry exists
// somewhere in the list. If neither the override file nor the embedded
// defaults produce a match (e.g., no provider env var is set), we fall
// back to DefaultModel so the resulting request fails with a clear
// provider-not-configured error at Generate time rather than routing
// silently through an unintended provider.
func resolveModel(def AgentDef) string {
	if def.Model != "" {
		return def.Model
	}
	if def.Capability != "" {
		if cfg, err := LoadModelConfig(); err == nil {
			if model := cfg.ResolveTier(def.Capability, DetectProviders()); model != "" {
				return model
			}
		}
	}
	return DefaultModel
}

// schemaRegistry maps output_schema type names to example struct instances
// for JSON schema generation. The struct is marshaled to JSON to produce
// the schema the LLM should conform to.
//
// TODO: Replace with jsonschema-go reflection once description tags are added
// to spec types. For now, use JSON examples as schema documentation.
var schemaRegistry = map[string]any{}

// RegisterSchema adds a type to the schema registry for output_schema injection.
func RegisterSchema(name string, example any) {
	schemaRegistry[name] = example
}
