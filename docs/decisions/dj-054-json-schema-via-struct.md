## DJ-054: JSON Schema via Struct Tags and Registry

**Status:** shipped (library claim below superseded by DJ-118; struct-tag + registry pattern remains valid)

**Decision:** Agent frontmatter can specify `output_schema: MasterPlan` (or other registered type name). At `BuildGenerateRequest` time, Go reflects the corresponding type and appends a JSON schema to the system prompt. Struct tags (`jsonschema:"description=..."`) provide field-level documentation. A `schemaRegistry` maps type names to example instances.

**Why:** LLMs produce more reliable structured output when given an explicit schema, and descriptions next to fields keep the schema in sync with Go code. The alternative — inlining schemas as Markdown in agent `.md` files — drifts from the Go types over time.

**Pattern:** `github.com/google/jsonschema-go` (already a transitive MCP SDK dependency) handles reflection. Equivalent to Pydantic's `Field(description="...")` in Python.

> ⚠ **Library claim superseded by DJ-118.** Implementation chose `github.com/invopop/jsonschema` (not google) for its richer struct-tag vocabulary (`enum=`, `minItems=`, `description=` as key=value pairs vs google's single-description-string model). See DJ-118 for the technical reasoning and reversal criteria.
