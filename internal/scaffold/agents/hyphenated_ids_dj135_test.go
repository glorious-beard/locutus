// DJ-135 — every scaffolded agent uses hyphenated ids. Per
// resolved-question 8, Claude Code requires hyphens; Gemini and
// Codex accept both; hyphens are the only naming convention that
// works on all three runtimes. The publisher in Phase 4 emits files
// per-runtime; authoring canonical agents in the hyphenated form
// from the start removes the need for a runtime translation pass.
//
// This test walks every internal/scaffold/agents/*.md, parses the
// frontmatter id: field, and asserts it contains no underscores. It
// also asserts the file's basename matches the id (sans .md extension).
// Both invariants together prevent the failure mode where the
// frontmatter id is updated but the on-disk filename isn't (or vice
// versa) — the publisher in Phase 4 keys off the id, and a stale
// filename would mean the canonical agent and its publishable copy
// don't agree on which name they share.

package agents_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var frontmatterIDRegexp = regexp.MustCompile(`(?m)^id:\s*([a-zA-Z0-9_-]+)\s*$`)

func TestAllAgentsHaveHyphenatedID_DJ135(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	entries, err := os.ReadDir(wd)
	require.NoError(t, err)

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(wd, e.Name())
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		// Find id: in the frontmatter (between first two --- delimiters).
		// We don't need a full YAML parser for this — the id is one
		// line and the regex anchors on line start.
		matches := frontmatterIDRegexp.FindStringSubmatch(string(body))
		require.Greaterf(t, len(matches), 1, "agent file %s has no id: frontmatter field", e.Name())
		id := matches[1]

		assert.NotContainsf(t, id, "_",
			"agent %s has underscored id %q — per DJ-135 resolved-question 8, agent ids must be hyphenated for Claude Code compatibility",
			e.Name(), id)

		// Filename minus .md must equal id so the publisher's "open
		// <id>.md" path resolution doesn't desync from the id the
		// canonical asserts.
		baseNoExt := strings.TrimSuffix(e.Name(), ".md")
		assert.Equalf(t, id, baseNoExt,
			"agent %s: id %q does not match filename %q (rename one or the other)",
			e.Name(), id, baseNoExt)

		checked++
	}
	assert.Greater(t, checked, 0, "no agent files inspected — test setup is wrong")
	t.Logf("checked %d agent files", checked)
}
