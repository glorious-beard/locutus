// DJ-136 phase 1 — orphan-overlay prevention.
//
// Walks the embedded internal/scaffold/plans/ directory. For every
// .<runtime>.md overlay, asserts that a sibling default <activity>.md
// exists and that the <runtime> token matches a key in
// internal/dispatch/acp.AgentSpawns. Catches drift like:
//
//   - typo'd overlay names ("spec_refinement.claudecode.md" without
//     the hyphen) that would never be read by the loader,
//   - orphan overlays whose default got deleted in a refactor,
//   - overlays for runtimes that don't exist (yet).

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listPlanFiles returns every .md filename in the plans/ dir (this
// package's own directory at test time — go test sets cwd to the
// package directory).
func listPlanFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// knownModes are the playbook resolution-mode suffixes (DJ-140).
// They may trail the runtime token in a filename
// (<activity>.<runtime>.<mode>.md or <activity>.<mode>.md) and are
// stripped before the runtime token is read.
var knownModes = []string{"interactive", "headless"}

// parsePlanName splits a plan filename into (activity, runtime, isOverlay).
// "spec_refinement.md"                          → ("spec_refinement", "",            false)
// "spec_refinement.claude-code.md"              → ("spec_refinement", "claude-code", true)
// "spec_refinement.claude-code.interactive.md"  → ("spec_refinement", "claude-code", true)
// "spec_refinement.interactive.md"              → ("spec_refinement", "",            false)
//
// A trailing mode suffix (DJ-140) is stripped before reading the
// runtime token, so a provider+mode overlay still validates its
// runtime against AgentSpawns. A bare <activity>.<mode>.md (no
// provider) is mode-only — it has no runtime to validate.
func parsePlanName(name string) (activity, runtime string, isOverlay bool) {
	stem := strings.TrimSuffix(name, ".md")
	// Strip a trailing known mode suffix (".interactive" / ".headless").
	for _, m := range knownModes {
		if strings.HasSuffix(stem, "."+m) {
			stem = strings.TrimSuffix(stem, "."+m)
			break
		}
	}
	// The activity name is snake_case. Runtime ids are hyphen-case or
	// single-word lowercase (claude-code / codex / gemini). The split
	// is on the first '.' after the stem — since activity names never
	// contain '.', anything after the first '.' is the runtime token.
	dot := strings.Index(stem, ".")
	if dot < 0 {
		return stem, "", false
	}
	return stem[:dot], stem[dot+1:], true
}

// TestPlaybookOverlay_NoOrphanOverlays — every .<runtime>.md has a
// sibling .md default, and the runtime token is a registered
// AgentSpawns key.
func TestPlaybookOverlay_NoOrphanOverlays(t *testing.T) {
	files := listPlanFiles(t)
	require.NotEmpty(t, files, "internal/scaffold/plans/ must contain at least one .md")

	have := map[string]bool{}
	for _, f := range files {
		have[f] = true
	}

	for _, f := range files {
		activity, runtime, isOverlay := parsePlanName(f)
		if !isOverlay {
			continue
		}
		// 1. Default sibling required.
		assert.Truef(t, have[activity+".md"],
			"overlay %q has no sibling default %q — every overlay must have a default to fall back to",
			f, activity+".md")
		// 2. Runtime must be a known AgentSpawns key.
		_, known := acp.AgentSpawns[runtime]
		assert.Truef(t, known,
			"overlay %q uses runtime %q which is not in acp.AgentSpawns — overlays must target a registered runtime",
			f, runtime)
	}
}

// TestPlaybookOverlay_DriftInvariants — overlays must only reference
// `mcp__locutus__*` tools and hyphenated `spec-*` agents that the
// default playbook also references. The check is subset, not strict
// equality — the canonical case is a thin overlay that delegates to
// the runtime's slash command (the slash command body is the default
// playbook, so the agent reaches the same tools / agents through
// that path). The invariant catches drift: overlays that mention a
// renamed tool / a retired agent / a typo'd id, all of which would
// only manifest when the overlay ran.
func TestPlaybookOverlay_DriftInvariants(t *testing.T) {
	files := listPlanFiles(t)
	for _, f := range files {
		activity, runtime, isOverlay := parsePlanName(f)
		if !isOverlay {
			continue
		}
		defaultPath := activity + ".md"
		// Defensive: if the orphan test failed already this comparison
		// is moot. require.FileExists keeps the failure clean.
		require.FileExists(t, defaultPath)

		defBody, err := os.ReadFile(defaultPath)
		require.NoError(t, err)
		ovBody, err := os.ReadFile(f)
		require.NoError(t, err)

		defTools := toSet(extractTokens(string(defBody), tokenMCPTool))
		for _, t2 := range extractTokens(string(ovBody), tokenMCPTool) {
			assert.Containsf(t, defTools, t2,
				"overlay %s references mcp tool %q not present in default %s — overlays must not introduce tools the default doesn't know about",
				f, t2, defaultPath)
		}

		defAgents := toSet(extractTokens(string(defBody), tokenHyphenAgent))
		for _, a := range extractTokens(string(ovBody), tokenHyphenAgent) {
			assert.Containsf(t, defAgents, a,
				"overlay %s references agent id %q not present in default %s — overlays must not introduce agents the default doesn't know about",
				f, a, defaultPath)
		}

		_ = runtime // runtime carried for future invariants (e.g. /goal presence on claude-code)
	}
}

func toSet(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}

// extractTokens returns the deduplicated, sorted set of tokens of a
// given kind found in body. Implementation is simple substring scan
// rather than a markdown parser — playbooks are unstructured prose
// and the tokens are unambiguous (mcp__ prefix, spec- prefix).
type tokenKind int

const (
	tokenMCPTool tokenKind = iota
	tokenHyphenAgent
)

func extractTokens(body string, kind tokenKind) []string {
	seen := map[string]struct{}{}
	switch kind {
	case tokenMCPTool:
		// "mcp__locutus__<name>" up to next non-[a-zA-Z0-9_] rune.
		const prefix = "mcp__locutus__"
		for i := 0; i < len(body); {
			j := strings.Index(body[i:], prefix)
			if j < 0 {
				break
			}
			start := i + j
			end := start + len(prefix)
			for end < len(body) && isIdentRune(body[end]) {
				end++
			}
			if end > start+len(prefix) {
				seen[body[start:end]] = struct{}{}
			}
			i = end
		}
	case tokenHyphenAgent:
		// "spec-<name>" — the canonical hyphenated agent ids. The
		// activity playbooks reference these as bare tokens (e.g.
		// "Dispatch `spec-scout`."), so substring scan on the prefix
		// is sufficient.
		const prefix = "spec-"
		for i := 0; i < len(body); {
			j := strings.Index(body[i:], prefix)
			if j < 0 {
				break
			}
			start := i + j
			end := start + len(prefix)
			for end < len(body) && isIdentRune(body[end]) {
				end++
			}
			if end > start+len(prefix) {
				token := body[start:end]
				// Avoid false positives on hyphens used in prose (e.g.
				// "spec-graph", "spec-refinement") — the canonical ids
				// are hyphenated and end in a recognized role word.
				if isLikelyAgentID(token) {
					seen[token] = struct{}{}
				}
			}
			i = end
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func isIdentRune(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

// isLikelyAgentID filters substring hits to plausible agent ids
// (spec-scout, spec-decision-elaborator, etc.). Conservative
// allowlist of role suffixes the canonical agent set uses; expanded
// when a new role lands. False negatives here only matter if a
// playbook references a new agent whose suffix isn't on the list —
// that is the desired failure mode (forces the list to evolve with
// the agent set).
func isLikelyAgentID(token string) bool {
	roles := []string{
		"-scout",
		"-decision-elaborator",
		"-feature-elaborator",
		"-strategy-elaborator",
		"-candidate-survey",
		"-coverage-critic",
		"-critic-elaborator",
		"-reconciler",
		"-summarizer",
		"-architect",
		"-challenger",
		"-advocate",
		"-finding-clusterer",
	}
	for _, r := range roles {
		if strings.HasSuffix(token, r) {
			return true
		}
	}
	return false
}

