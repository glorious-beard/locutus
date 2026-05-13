package cmd

// TestSchemaDescriptionsAvoidPlaceholderPriming guards against the
// anti-pattern documented in docs/agent-conventions.md: naming a
// forbidden token in a jsonschema description primes the model to
// emit exactly that token. We hit this with the spec_challenger
// agent — three description tags read "NOT a placeholder like
// 'dummy' or 'TBD'", and the model emitted {"weakness":"dummy",
// "evidence":"dummy","counterproposal":"dummy"} on its first call.
//
// The test walks every registered schema (the agent package's
// init() plus cascade's init() for RewriteResult), recursively
// visits every "description" field in the reflected JSON Schema,
// and fails if any description contains a known placeholder token.
// Lives in cmd/ because it needs both internal/agent and
// internal/cascade in the import graph — placing it in either
// package alone causes an import cycle.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent"
	// Pull cascade in so its init() registers RewriteResult before
	// the walk runs. The blank import is load-bearing.
	_ "github.com/chetan/locutus/internal/cascade"
)

// forbiddenPlaceholderTokens names the literal strings observed (or
// likely to be observed) when the model short-circuits to schema-
// skeleton emit mode. Matched with word boundaries so "bare features"
// (containing "bar") and "Bazel" don't false-positive. Keep in sync
// with agent.challengerPlaceholderTokens.
var forbiddenPlaceholderTokens = []string{
	"dummy",
	"placeholder",
	"tbd",
	"foo",
	"bar",
	"baz",
	"lorem",
	"ipsum",
}

// forbiddenPattern compiles forbiddenPlaceholderTokens into a single
// case-insensitive word-boundary regex. Word boundaries (\b) prevent
// matches inside real words ("bare", "Bazel", "footguns") and keep
// matches on real tokens ("foo.go", "Don't emit placeholder shapes").
var forbiddenPattern = regexp.MustCompile(`(?i)\b(` + strings.Join(forbiddenPlaceholderTokens, "|") + `)\b`)

func TestSchemaDescriptionsAvoidPlaceholderPriming(t *testing.T) {
	names := agent.RegisteredSchemaNames()
	require.NotEmpty(t, names, "no schemas registered — package init order regression?")

	for _, name := range names {
		schema, err := agent.SchemaFor(name)
		require.NoErrorf(t, err, "schema %q failed to reflect", name)
		walkDescriptions(t, name, "", schema)
	}
}

// TestAgentPromptsAvoidPlaceholderPriming guards the same anti-
// pattern on the prompt side. An agent system prompt that lectures
// the model with "do not emit `decisions: [{}]` placeholders" puts
// the forbidden token into the model's attention window — the same
// failure mode as a schema description naming "dummy".
//
// Scoped to agents that declare an output_schema (i.e., emit
// structured JSON). Agents like validator.md whose job is to grep
// FOR these tokens in code are intentionally out of scope: the
// tokens describe the inputs the validator examines, not the
// values the validator emits.
func TestAgentPromptsAvoidPlaceholderPriming(t *testing.T) {
	agentsDir := filepath.Join("..", "internal", "scaffold", "agents")
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err, "read agents directory")

	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(agentsDir, e.Name())
		data, err := os.ReadFile(path)
		require.NoErrorf(t, err, "read %s", path)
		body := string(data)

		// Only scan agents that emit structured output. Free-form
		// agents (validator.md) intentionally name these tokens to
		// describe input patterns to look for.
		if !strings.Contains(body, "\noutput_schema:") && !strings.HasPrefix(body, "output_schema:") {
			continue
		}
		scanned++

		for i, line := range strings.Split(body, "\n") {
			if m := forbiddenPattern.FindString(line); m != "" {
				t.Errorf("%s:%d names forbidden token %q — anti-pattern priming per docs/agent-conventions.md. Rewrite with positive phrasing (what the agent SHOULD emit) instead of negative phrasing (what it must NOT emit). Line: %q",
					e.Name(), i+1, strings.ToLower(m), strings.TrimSpace(line))
			}
		}
	}
	require.Greater(t, scanned, 0, "no output_schema agents scanned — directory layout regression?")
}

func walkDescriptions(t *testing.T, schemaName, path string, node any) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if desc, ok := v["description"].(string); ok {
			if m := forbiddenPattern.FindString(desc); m != "" {
				t.Errorf("schema %q field %q description names forbidden token %q — anti-pattern priming per docs/agent-conventions.md. Rewrite with positive phrasing (what the field IS) instead of negative phrasing (what it must NOT be). Description: %q",
					schemaName, path, strings.ToLower(m), desc)
			}
		}
		for k, child := range v {
			childPath := path
			if k == "properties" {
				// descend into property bag without prefixing
			} else if path == "" {
				childPath = k
			} else {
				childPath = path + "." + k
			}
			walkDescriptions(t, schemaName, childPath, child)
		}
	case []any:
		for _, item := range v {
			walkDescriptions(t, schemaName, path, item)
		}
	}
}
