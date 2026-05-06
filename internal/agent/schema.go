package agent

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/invopop/jsonschema"
)

// schemaRegistry maps output_schema type names (as referenced in
// AgentDef.OutputSchema) to example struct values. Reflection on the
// example produces a JSON Schema each adapter projects into its
// provider-native strict-mode shape:
//
//   - Anthropic: forced tool-use with the schema as the tool's
//     input_schema.
//   - Gemini: GenerateContentConfig.ResponseSchema.
//   - OpenAI Responses: response_format json_schema strict:true.
//
// The example also serves as documentation embedded in the system
// prompt — providers without strict-mode coverage still get a
// concrete shape to target.
var (
	schemaMu        sync.RWMutex
	schemaRegistry  = map[string]any{}
	schemaOverrides = map[string]map[string]any{}

	schemaCacheMu sync.Mutex
	schemaCache   = map[string]map[string]any{}
)

// RegisterSchema adds an example struct to the registry under name.
// Called from package-level init() in schemas.go.
func RegisterSchema(name string, example any) {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	schemaRegistry[name] = example
}

// RegisterSchemaOverride installs a hand-authored JSON Schema for
// name, bypassing reflection-based generation. Use when the Go
// example struct can't express the constraint the adapters need to
// enforce — discriminated unions in particular: a struct whose
// fields are all `,omitempty` reflects to a permissive schema, but
// the agent's prompt requires different field subsets per kind. A
// hand-authored schema with `oneOf` discriminated by `kind` lets the
// API reject malformed actions.
//
// Override takes precedence over RegisterSchema's reflected output;
// SchemaExample (used by the prompt-doc renderer) still returns the
// reflected example for inline documentation.
func RegisterSchemaOverride(name string, schema map[string]any) {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	schemaOverrides[name] = schema
}

// SchemaExample returns the example struct registered under name and
// a presence flag. Used by the prompt builder when documenting the
// expected output shape inline.
func SchemaExample(name string) (any, bool) {
	schemaMu.RLock()
	defer schemaMu.RUnlock()
	v, ok := schemaRegistry[name]
	return v, ok
}

// SchemaFor returns the JSON Schema (as a generic map) reflected
// from the registered example struct. Caches per name so reflection
// runs once per process. AdditionalProperties:false is baked in at
// every object level via enforceStrict; the `required` list comes
// from invopop reflection over `,omitempty` JSON tags so optional
// fields stay optional.
//
// Returns a deep copy of the cached schema on every call. Adapters
// alias inner maps into provider-specific param types (e.g.
// anthropic-sdk-go's ToolInputSchemaParam.Properties) and SDKs are
// free to mutate downstream; sharing the cached map across calls
// would let one mutation poison every concurrent reader.
func SchemaFor(name string) (map[string]any, error) {
	schemaCacheMu.Lock()
	if cached, ok := schemaCache[name]; ok {
		schemaCacheMu.Unlock()
		return deepCopySchema(cached), nil
	}
	schemaCacheMu.Unlock()

	// Hand-authored override wins over reflection. Used for
	// constraints invopop can't express (discriminated unions, etc.).
	schemaMu.RLock()
	override, hasOverride := schemaOverrides[name]
	schemaMu.RUnlock()
	if hasOverride {
		schema := deepCopySchema(override)
		stripJSONSchemaArtifacts(schema)
		schemaCacheMu.Lock()
		schemaCache[name] = schema
		schemaCacheMu.Unlock()
		return deepCopySchema(schema), nil
	}

	example, ok := SchemaExample(name)
	if !ok {
		return nil, fmt.Errorf("schema %q not registered", name)
	}

	r := jsonschema.Reflector{
		AllowAdditionalProperties:  false,
		DoNotReference:             true,
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: false,
	}
	reflected := r.Reflect(example)

	// Round-trip through JSON to land in a generic map shape the
	// adapters can mutate (Gemini wants genai.Schema, OpenAI wants
	// a json_schema with strict markers, Anthropic wants the
	// tool's input_schema). Each adapter post-processes from this
	// neutral map.
	data, err := json.Marshal(reflected)
	if err != nil {
		return nil, fmt.Errorf("schema %q marshal: %w", name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("schema %q unmarshal: %w", name, err)
	}
	stripJSONSchemaArtifacts(schema)
	enforceStrict(schema)

	schemaCacheMu.Lock()
	schemaCache[name] = schema
	schemaCacheMu.Unlock()
	return deepCopySchema(schema), nil
}

// deepCopySchema clones a JSON-Schema-shaped map[string]any
// recursively. Values are restricted to the JSON-decodable set
// (string, float64, bool, nil, []any, map[string]any) since the
// schema came in via json.Unmarshal — types outside that set are
// returned as-is, which would be a programming error in the
// caller (no mutable types in our schemas today).
func deepCopySchema(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopySchema(t)
	case []any:
		c := make([]any, len(t))
		for i, item := range t {
			c[i] = deepCopyValue(item)
		}
		return c
	default:
		return v
	}
}

// stripJSONSchemaArtifacts removes draft / version metadata that
// providers reject when validating json_schema strict requests. The
// $schema URI in particular trips OpenAI's strict-mode validator.
func stripJSONSchemaArtifacts(node map[string]any) {
	delete(node, "$schema")
	delete(node, "$id")
	delete(node, "$defs")
	delete(node, "definitions")
	for _, v := range node {
		switch t := v.(type) {
		case map[string]any:
			stripJSONSchemaArtifacts(t)
		case []any:
			for _, item := range t {
				if m, ok := item.(map[string]any); ok {
					stripJSONSchemaArtifacts(m)
				}
			}
		}
	}
}

// enforceStrict walks the schema tree and ensures every object node
// carries additionalProperties:false. It does NOT rewrite required:
// the invopop reflector already produces the correct list from
// `,omitempty` JSON tags (RequiredFromJSONSchemaTags:false treats
// omitempty as "not required"). Forcing every property required
// would break agents whose example struct has mutually-exclusive
// or context-conditional fields — e.g. ReconciliationAction's
// Canonical / Loser / RejectedBecause / ExistingID, where each
// action kind populates a different subset.
//
// OpenAI's strict json_schema mode separately requires every
// property in `required` and uses `["type", "null"]` unions for
// optional fields. The OpenAI Responses adapter detects partial-
// required schemas and drops Strict:true rather than fabricating
// values; cheaper than rewriting Go example structs to add null
// unions everywhere.
func enforceStrict(node map[string]any) {
	if node == nil {
		return
	}
	if t, ok := node["type"].(string); ok && t == "object" {
		node["additionalProperties"] = false
		if props, ok := node["properties"].(map[string]any); ok {
			for _, v := range props {
				if child, ok := v.(map[string]any); ok {
					enforceStrict(child)
				}
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		enforceStrict(items)
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := node[key].([]any); ok {
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					enforceStrict(m)
				}
			}
		}
	}
}

// SchemaPromptDoc returns the indented JSON of the registered
// example for inline documentation in the agent's system prompt.
// Rendered to providers without strict-mode coverage as a concrete
// shape to target. Empty string when no example is registered.
func SchemaPromptDoc(name string) string {
	example, ok := SchemaExample(name)
	if !ok {
		return ""
	}
	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}
