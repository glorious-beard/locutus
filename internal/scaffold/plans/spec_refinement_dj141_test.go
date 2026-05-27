// DJ-141 — Step 0 applies the matcher's new categories: `promoted`
// (revise the unanchored node to set source_clause + clear origin),
// `contradicted` (delete the opposite-polarity node, propose the new
// one, surface citing nodes as at-risk), and mints bootstrap nodes as
// unanchored with origin.
package plans_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpecRefinementPlaybookDescribesProvenanceMoves(t *testing.T) {
	body := strings.ToLower(loadSpecRefinement(t))
	for _, term := range []string{"promoted", "contradicted", "unanchored", "origin"} {
		assert.Contains(t, body, term, "Step 0 must describe %q (DJ-141)", term)
	}
}

func TestSpecRefinementPlaybookBootstrapMintsUnanchored(t *testing.T) {
	body := strings.ToLower(loadSpecRefinement(t))
	assert.Contains(t, body, "unanchored",
		"bootstrap step must mint decision/mission-derived nodes as unanchored")
	assert.Contains(t, body, "never fabricate",
		"bootstrap step must forbid fabricating a source_clause from decision bodies")
}
