package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaProsePromptDocRendersScoutBrief exercises the
// reflection-based prose generator on a real registered schema
// (ScoutBrief, the spec-scout's output). Asserts on structural
// landmarks rather than byte-equality — the renderer's exact
// formatting may evolve, but every top-level field must appear as a
// ## section, every field's jsonschema description must show up as
// context, and every example value must render below as labeled
// content. Defends against silent breakage of the generator on
// schema evolution.
func TestSchemaProsePromptDocRendersScoutBrief(t *testing.T) {
	out := SchemaProsePromptDoc("ScoutBrief")
	require.NotEmpty(t, out, "ScoutBrief is registered with an example payload; generator must produce prose")

	// Every top-level field appears as a ## section.
	for _, field := range []string{
		"domain_read", "technology_options", "implicit_assumptions",
		"watch_outs", "axes_open", "new_nodes", "critique_dimensions",
		"concern_dispositions", "converged",
	} {
		assert.Contains(t, out, "## "+field,
			"field %q should appear as a top-level ## section", field)
	}

	// jsonschema descriptions reach the model as section context.
	assert.Contains(t, out, "A paragraph or two describing what the scout believes",
		"domain_read's jsonschema description renders below the heading")
	assert.Contains(t, out, "Foundational axes the scout identified",
		"axes_open's jsonschema description renders below the heading")

	// Example values render under "Example: " blocks.
	assert.Contains(t, out, "Campaign software for political organizing",
		"domain_read's registered example string renders inline as Example:")
	assert.Contains(t, out, `"auth-provider"`,
		"axes_open's first entry id renders as a labeled bullet")
	assert.Contains(t, out, `"feat-realtime-turf-dashboard"`,
		"new_nodes' first entry id renders inside the labeled list")

	// Nested struct fields render as labeled bullets at depth.
	assert.Contains(t, out, "- id:",
		"struct entries in slices render with `- field: value` labeling")
	assert.Contains(t, out, "- description:")
	assert.Contains(t, out, "- source_evidence:")

	// No literal JSON syntax that would prime JSON-mode output —
	// no curly braces, no ```json fences, no leading [.
	assert.NotContains(t, out, "```json",
		"prose example must not include a JSON code fence (defeats the whole point)")
	assert.NotContains(t, out, "{\n  \"",
		"prose example must not contain a JSON object literal layout")
}

// TestSchemaProsePromptDocUnknown returns empty for unregistered
// schemas so callers can safely fall through to no-example behavior.
func TestSchemaProsePromptDocUnknown(t *testing.T) {
	assert.Empty(t, SchemaProsePromptDoc("NoSuchSchema"))
}

// TestSchemaProsePromptDocScalarExample handles schemas where the
// example is itself a scalar (a string, an int) rather than a
// struct. Returns empty so the caller doesn't get a malformed prose
// dump — there's nothing meaningful to render at the top level
// without field names to anchor sections against.
func TestSchemaProsePromptDocScalarExample(t *testing.T) {
	RegisterSchema("ScalarProseTest", "just a string")
	defer func() {
		schemaMu.Lock()
		delete(schemaRegistry, "ScalarProseTest")
		schemaMu.Unlock()
	}()
	assert.Empty(t, SchemaProsePromptDoc("ScalarProseTest"),
		"scalar examples have no fields to anchor sections; renderer falls through to empty")
}
