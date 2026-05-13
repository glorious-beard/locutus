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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent"
	// Pull cascade in so its init() registers RewriteResult before
	// the walk runs. The blank import is load-bearing.
	_ "github.com/chetan/locutus/internal/cascade"
)

func TestSchemaDescriptionsAvoidPlaceholderPriming(t *testing.T) {
	// Tokens drawn from the validators that catch degenerate output
	// after the fact (challengerPlaceholderTokens in justify.go).
	// Keep in sync if the validator list changes.
	forbidden := []string{
		"dummy",
		"placeholder",
		"tbd",
		"foo",
		"bar",
		"baz",
		"lorem",
		"ipsum",
	}

	names := agent.RegisteredSchemaNames()
	require.NotEmpty(t, names, "no schemas registered — package init order regression?")

	for _, name := range names {
		schema, err := agent.SchemaFor(name)
		require.NoErrorf(t, err, "schema %q failed to reflect", name)
		walkDescriptions(t, name, "", schema, forbidden)
	}
}

func walkDescriptions(t *testing.T, schemaName, path string, node any, forbidden []string) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if desc, ok := v["description"].(string); ok {
			lower := strings.ToLower(desc)
			for _, tok := range forbidden {
				// Quote-bracketed mention is the strongest priming
				// signal — 'dummy', "dummy", `dummy` all surface
				// the literal token as a candidate value the model
				// can copy. Bare-word mention is also flagged
				// because the model treats it the same under
				// attention pressure.
				if strings.Contains(lower, tok) {
					t.Errorf("schema %q field %q description names forbidden token %q — anti-pattern priming per docs/agent-conventions.md. Rewrite with positive phrasing (what the field IS) instead of negative phrasing (what it must NOT be). Description: %q",
						schemaName, path, tok, desc)
				}
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
			walkDescriptions(t, schemaName, childPath, child, forbidden)
		}
	case []any:
		for _, item := range v {
			walkDescriptions(t, schemaName, path, item, forbidden)
		}
	}
}
