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

// DefaultModels maps capability tiers to Anthropic model strings. Provider
// prefix is required — Genkit routes to the registered plugin by parsing
// the string before the slash. Users with GEMINI_API_KEY but no
// ANTHROPIC_API_KEY will have these rewritten to googleai/... at
// Generate time by GenKitLLM.resolveModel, so these values act as the
// "preferred default" when both providers are available.
var DefaultModels = map[CapabilityTier]string{
	CapabilityFast:     "anthropic/claude-haiku-4-5-20251001",
	CapabilityBalanced: "anthropic/claude-sonnet-4-6",
	CapabilityStrong:   "anthropic/claude-opus-4-7",
}

// DefaultModel is the fallback model when no capability tier is specified.
const DefaultModel = "anthropic/claude-sonnet-4-6"

// GoogleAIDefaultModels is the equivalent tier→model map for the Google AI
// plugin, used as a fallback when Anthropic is not configured. Model
// strings are current as of 2026; users can override via LOCUTUS_MODEL
// or per-agent def.
var GoogleAIDefaultModels = map[CapabilityTier]string{
	CapabilityFast:     "googleai/gemini-2.5-flash-lite",
	CapabilityBalanced: "googleai/gemini-2.5-flash",
	CapabilityStrong:   "googleai/gemini-2.5-pro",
}

// AgentDef is an agent definition loaded from a .md file.
type AgentDef struct {
	ID           string         `yaml:"id"`
	Role         string         `yaml:"role"`
	Model        string         `yaml:"model,omitempty"`
	Capability   CapabilityTier `yaml:"capability,omitempty"`
	Temperature  float64        `yaml:"temperature,omitempty"`
	OutputSchema string         `yaml:"output_schema,omitempty"` // type name for JSON schema injection
	SystemPrompt string         // the markdown body (not from YAML)
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

	// Append JSON schema if output_schema is specified.
	if def.OutputSchema != "" {
		if schema, ok := schemaRegistry[def.OutputSchema]; ok {
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
		Model:       model,
		Messages:    msgs,
		Temperature: def.Temperature,
	}
}

// resolveModel determines the model string for an agent.
// Priority: explicit Model field > Capability tier > DefaultModel.
func resolveModel(def AgentDef) string {
	if def.Model != "" {
		return def.Model
	}
	if def.Capability != "" {
		if model, ok := DefaultModels[def.Capability]; ok {
			return model
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
