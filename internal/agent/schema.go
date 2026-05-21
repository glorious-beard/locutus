package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
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

// RegisteredSchemaNames returns the names of every schema registered
// via RegisterSchema or RegisterSchemaOverride. Order is not stable.
// Used by the convention guard test (schema_conventions_test.go) to
// walk every shipping schema and assert tag-level discipline.
func RegisteredSchemaNames() []string {
	schemaMu.RLock()
	defer schemaMu.RUnlock()
	seen := make(map[string]struct{}, len(schemaRegistry)+len(schemaOverrides))
	for name := range schemaRegistry {
		seen[name] = struct{}{}
	}
	for name := range schemaOverrides {
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
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
//
// Also strips `examples` keys. Example payloads belong in the prompt
// (via SchemaPromptDoc + BuildSystemPrompt for thinking-off agents)
// rather than the strict-mode schema — the Anthropic docs explicitly
// list `minLength`/`maxLength`/`pattern`/etc. as constraints the SDK
// transforms away, and while `examples` isn't explicitly listed,
// keeping it in the schema risks both priming the model with literal
// example values and rejection by stricter provider validators. Defense
// in depth: nothing in the codebase currently uses `example=` tags, but
// future struct authors who add them shouldn't accidentally leak them
// into provider strict-mode.
func stripJSONSchemaArtifacts(node map[string]any) {
	delete(node, "$schema")
	delete(node, "$id")
	delete(node, "$defs")
	delete(node, "definitions")
	delete(node, "examples")
	delete(node, "example")
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
//
// Superseded for BuildSystemPrompt by SchemaFieldHints (the labeled-
// prose rendering of the reflected schema), but retained as a
// standalone helper for callers that still want a concrete example
// payload.
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

// SchemaProsePromptDoc returns a labeled-prose rendering of the
// registered example for inline documentation in agent prompts and
// user messages. Each top-level field becomes a ## section: the
// jsonschema `description=` tag value supplies the section context,
// and the example value renders below it as an "Example: ..." block
// (scalars inline; structs/slices as nested labeled lists).
//
// Used in three places:
//
//   - Single-call agents (thinking off + schema): the executor
//     prepends this as a Cacheable user message before the projected
//     input so the model sees a worked content shape in prose form
//     without the JSON dump priming JSON-mode output.
//   - Reasoning pass of the DJ-130 split: same plumbing; the prose
//     example demonstrates the kind of narrative the reasoner should
//     produce, complementing the explicit prose directive that
//     trails the projected input.
//   - Format pass of the split: emitted alongside the JSON example
//     so the formatter sees a one-shot pairing (prose-shaped input →
//     JSON-shaped output) for the same example content.
//
// Returns "" when no example is registered or when the example isn't
// a struct (a JSON example exists but prose rendering needs named
// fields to anchor against).
func SchemaProsePromptDoc(name string) string {
	example, ok := SchemaExample(name)
	if !ok {
		return ""
	}
	v := reflect.ValueOf(example)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	var b strings.Builder
	renderProseStruct(&b, v)
	return strings.TrimRight(b.String(), "\n")
}

// renderProseStruct walks a struct value's exported fields and emits
// each as a `## <name>` section with the field's jsonschema
// description and an "Example: " block. Top-level entry point for
// the generator; nested structs go through renderProseFields below
// (which uses labeled-list shape instead of headings).
func renderProseStruct(b *strings.Builder, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := proseJSONName(f)
		if name == "" || name == "-" {
			continue
		}
		fmt.Fprintf(b, "## %s\n\n", name)
		if desc := proseExtractDescription(f.Tag.Get("jsonschema")); desc != "" {
			b.WriteString(desc)
			b.WriteString("\n\n")
		}
		b.WriteString("Example: ")
		renderProseExampleBlock(b, v.Field(i), 0)
		b.WriteString("\n\n")
	}
}

// renderProseExampleBlock renders one field's example value as either
// an inline scalar (for strings/numbers/bools) on the same line as
// "Example: ", or a multi-line list/struct under it. depth controls
// list indentation for nested values.
func renderProseExampleBlock(b *strings.Builder, v reflect.Value, depth int) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			b.WriteString("(none)")
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Bool:
		b.WriteString(proseFormatScalar(v))
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			b.WriteString("(none)")
			return
		}
		b.WriteString("\n")
		renderProseSliceEntries(b, v, depth+1)
	case reflect.Struct:
		b.WriteString("\n")
		renderProseFields(b, v, depth+1)
	case reflect.Map:
		if v.Len() == 0 {
			b.WriteString("(none)")
			return
		}
		b.WriteString("\n")
		renderProseMapEntries(b, v, depth+1)
	default:
		b.WriteString("(unsupported)")
	}
}

// renderProseSliceEntries renders each slice entry as a top-level
// bullet at depth-indent. Struct entries expand their fields as
// nested labeled bullets one level deeper; scalar entries render
// inline on the bullet. We render every entry so the example shows
// the full variety the registered fixture authored; registered
// examples are short by convention (1-3 entries each).
func renderProseSliceEntries(b *strings.Builder, v reflect.Value, depth int) {
	indent := strings.Repeat("  ", depth-1)
	for i := 0; i < v.Len(); i++ {
		entry := v.Index(i)
		for entry.Kind() == reflect.Pointer {
			if entry.IsNil() {
				break
			}
			entry = entry.Elem()
		}
		switch entry.Kind() {
		case reflect.Struct:
			fmt.Fprintf(b, "%s- entry:\n", indent)
			renderProseFields(b, entry, depth+1)
		default:
			fmt.Fprintf(b, "%s- ", indent)
			renderProseExampleBlock(b, entry, depth)
			b.WriteString("\n")
		}
	}
}

// renderProseFields renders a struct's exported fields as labeled
// bullets at depth-indent. Used for nested structs (slice entries,
// map values, fields inside a parent struct). Top-level structs go
// through renderProseStruct's heading form instead.
func renderProseFields(b *strings.Builder, v reflect.Value, depth int) {
	indent := strings.Repeat("  ", depth-1)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := proseJSONName(f)
		if name == "" || name == "-" {
			continue
		}
		fv := v.Field(i)
		for fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				break
			}
			fv = fv.Elem()
		}
		fmt.Fprintf(b, "%s- %s: ", indent, name)
		switch fv.Kind() {
		case reflect.Slice, reflect.Array:
			if fv.Len() == 0 {
				b.WriteString("(none)\n")
				continue
			}
			b.WriteString("\n")
			renderProseSliceEntries(b, fv, depth+1)
		case reflect.Struct:
			b.WriteString("\n")
			renderProseFields(b, fv, depth+1)
		case reflect.Map:
			if fv.Len() == 0 {
				b.WriteString("(none)\n")
				continue
			}
			b.WriteString("\n")
			renderProseMapEntries(b, fv, depth+1)
		default:
			b.WriteString(proseFormatScalar(fv))
			b.WriteString("\n")
		}
	}
}

// renderProseMapEntries renders map[K]V entries as `- key: value`
// bullets. Used rarely (most schemas use slices and structs); kept
// for completeness so reflection on any registered example doesn't
// fall through to "(unsupported)".
func renderProseMapEntries(b *strings.Builder, v reflect.Value, depth int) {
	indent := strings.Repeat("  ", depth-1)
	for _, key := range v.MapKeys() {
		fmt.Fprintf(b, "%s- %v: ", indent, key.Interface())
		renderProseExampleBlock(b, v.MapIndex(key), depth)
		b.WriteString("\n")
	}
}

// proseFormatScalar renders a scalar Value as the inline text that
// appears after "Example: " or "- name: ". Strings are quoted so
// multi-word values stay legible.
func proseFormatScalar(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return fmt.Sprintf("%q", v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	}
	return fmt.Sprintf("%v", v.Interface())
}

// proseJSONName returns the field's JSON-encoded name from its `json`
// struct tag, falling back to the Go field name when no tag is set.
// Returns "" / "-" for fields tagged out of JSON serialization so the
// renderer can skip them (matches `encoding/json`'s convention).
func proseJSONName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// proseExtractDescription pulls the `description=...` value out of an
// invopop-style jsonschema tag. Returns "" when the tag has no
// description= key. Convention in this codebase is that description=
// is the LAST key in the comma-separated tag (after enum=, minItems=,
// etc.), so taking everything after the first `description=` substring
// captures the full description including any internal commas.
func proseExtractDescription(tag string) string {
	idx := strings.Index(tag, "description=")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(tag[idx+len("description="):])
}

