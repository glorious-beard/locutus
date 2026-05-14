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

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent"
	// Pull every package whose init() registers a schema so all of
	// them are present at test time. The blank imports are load-
	// bearing: cascade registers RewriteResult; remediate registers
	// RemediationPlan; preflight registers PreflightReport; eval
	// registers LLMJudgeResult.
	_ "github.com/chetan/locutus/internal/cascade"
	_ "github.com/chetan/locutus/internal/eval"
	_ "github.com/chetan/locutus/internal/preflight"
	_ "github.com/chetan/locutus/internal/remediate"
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

// TestAgentSchemaReferencesResolve guards the cross-boundary integrity
// of the agent → schema contract. Every agent .md that declares
// `output_schema: X` must have a Go-side registration for X (via
// agent.RegisterSchema). Without this guard, an agent can ship
// referencing a phantom schema name (a fictional type that exists
// only in markdown), pass code review, pass tests using mocks that
// bypass the registration check, and then fail loudly the first time
// it's actually dispatched against a real model.
//
// This is the class of bug that surfaced when scout / backend_analyzer
// / frontend_analyzer / infra_analyzer / gap_analyst / remediator /
// preflight / llm_judge had been referencing types that didn't exist
// in Go for months — the tests passed because the test fixtures used
// minimal frontmatter without output_schema.
func TestAgentSchemaReferencesResolve(t *testing.T) {
	agentsDir := filepath.Join("..", "internal", "scaffold", "agents")
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err, "read agents directory")

	type minimalFrontmatter struct {
		ID           string `yaml:"id"`
		OutputSchema string `yaml:"output_schema"`
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(agentsDir, e.Name())
		data, err := os.ReadFile(path)
		require.NoErrorf(t, err, "read %s", path)

		var fm minimalFrontmatter
		if _, err := frontmatter.Parse(data, &fm); err != nil {
			t.Errorf("%s: frontmatter parse failed: %v", e.Name(), err)
			continue
		}
		if fm.OutputSchema == "" {
			continue
		}
		checked++

		if _, err := agent.SchemaFor(fm.OutputSchema); err != nil {
			t.Errorf(
				"%s declares output_schema: %q but no Go-side registration exists. "+
					"Either define the type and call agent.RegisterSchema(%q, ...) in the package that owns the consumer, "+
					"or drop the output_schema declaration from the agent's frontmatter. "+
					"(Original error: %v)",
				e.Name(), fm.OutputSchema, fm.OutputSchema, err,
			)
		}
	}
	require.Greater(t, checked, 0, "no agents with output_schema scanned — directory layout regression?")
}

// TestEveryAgentDeclaresThinking guards the explicit-thinking
// contract introduced when reasoning mode was decoupled from tier.
// Every agent .md must declare `thinking: off | on | high` in its
// frontmatter — no fallback, no implicit default. The whole point
// of the decoupling was to force every agent author to make the
// reasoning-mode choice deliberately per-agent; a missing declaration
// would silently default to off and re-create the "what does this
// agent want?" ambiguity we just eliminated.
//
// Without this guard, a new agent can ship without the choice being
// made — the dispatcher would treat empty-string thinking as off (the
// safer default), but the operator reading the agent .md would have
// no way to tell whether thinking-off was intended or accidental.
func TestEveryAgentDeclaresThinking(t *testing.T) {
	agentsDir := filepath.Join("..", "internal", "scaffold", "agents")
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err, "read agents directory")

	type minimalFrontmatter struct {
		ID       string `yaml:"id"`
		Thinking string `yaml:"thinking"`
	}

	allowed := map[string]bool{"off": true, "on": true, "high": true}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(agentsDir, e.Name())
		data, err := os.ReadFile(path)
		require.NoErrorf(t, err, "read %s", path)

		var fm minimalFrontmatter
		if _, err := frontmatter.Parse(data, &fm); err != nil {
			t.Errorf("%s: frontmatter parse failed: %v", e.Name(), err)
			continue
		}
		checked++
		if fm.Thinking == "" {
			t.Errorf("%s: missing `thinking:` declaration. Every agent must explicitly declare its reasoning mode (off / on / high) — the decoupling from tier removed the implicit default. Pick a value based on what the agent does.", e.Name())
			continue
		}
		if !allowed[fm.Thinking] {
			t.Errorf("%s: thinking: %q is not one of off / on / high.", e.Name(), fm.Thinking)
		}
	}
	require.Greater(t, checked, 0, "no agents scanned — directory layout regression?")
}

// TestSchemaExamplePayloadsAvoidPlaceholderPriming extends the
// placeholder-priming guard to cover the registered example payloads
// themselves. With Path A wired up (BuildSystemPrompt appends a JSON
// example for thinking-off + schema agents), the registered example
// payload reaches the model on every call — same priming surface as
// schema descriptions. An example like {Severity: "dummy"} would
// recreate exactly the priming bug the conventions-doc workflow is
// guarding against.
func TestSchemaExamplePayloadsAvoidPlaceholderPriming(t *testing.T) {
	names := agent.RegisteredSchemaNames()
	require.NotEmpty(t, names, "no schemas registered — package init order regression?")

	for _, name := range names {
		doc := agent.SchemaPromptDoc(name)
		if doc == "" {
			continue
		}
		for i, line := range strings.Split(doc, "\n") {
			if m := forbiddenPattern.FindString(line); m != "" {
				t.Errorf("schema %q example payload line %d names forbidden token %q — the example payload reaches the model verbatim via BuildSystemPrompt for thinking-off agents. Rewrite the registered example struct's field values to use descriptive prose instead of placeholder tokens. Line: %q",
					name, i+1, strings.ToLower(m), strings.TrimSpace(line))
			}
		}
	}
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
