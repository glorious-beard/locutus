// Decision-journal split (2026-05-26) — every per-decision file at
// docs/decisions/dj-NNN-<slug>.md must be referenced from the manifest
// table in docs/DECISION_JOURNAL.md, and every manifest row must point
// at an existing per-decision file. This test asserts that bijection.
//
// Without this, a contributor adding a per-decision file but forgetting
// the manifest row produces an orphan (or vice versa: a manifest row
// pointing at a deleted file produces a dead link). The split's value
// depends on the manifest being a reliable index, so we test it.
//
// The test is also a place to enforce the filename convention:
// dj-NNN-<slug>.md where NNN is three-digit zero-padded and slug is
// lowercase-hyphenated.

package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decisionFilenameRe enforces the naming convention: dj-NNN-slug.md,
// where NNN is three-digit zero-padded and slug is lowercase-hyphenated.
var decisionFilenameRe = regexp.MustCompile(`^dj-(\d{3})-[a-z0-9-]+\.md$`)

// manifestRowRe matches a manifest table row's open(...) link target.
// Example match: [open](decisions/dj-138-refine-with-bias-cascade.md)
var manifestRowRe = regexp.MustCompile(`\[open\]\(decisions/(dj-\d{3}-[a-z0-9-]+\.md)\)`)

// manifestAnchorRe matches the per-row HTML anchor: <a id="dj-NNN"></a>
// Asserts every per-decision file has a manifest anchor for external refs.
var manifestAnchorRe = regexp.MustCompile(`<a id="dj-(\d{3})">`)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	// Walk up until we find docs/DECISION_JOURNAL.md.
	dir := wd
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "docs", "DECISION_JOURNAL.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", wd)
	return ""
}

// TestEveryDecisionFileIsInManifest asserts every file under
// docs/decisions/ appears in the manifest's [open](...) link column.
func TestEveryDecisionFileIsInManifest(t *testing.T) {
	root := repoRoot(t)
	decisionsDir := filepath.Join(root, "docs", "decisions")
	manifestPath := filepath.Join(root, "docs", "DECISION_JOURNAL.md")

	entries, err := os.ReadDir(decisionsDir)
	require.NoError(t, err, "must be able to read docs/decisions/")

	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err, "must be able to read docs/DECISION_JOURNAL.md")
	manifest := string(manifestBytes)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		assert.Contains(t, manifest, "decisions/"+e.Name(),
			"per-decision file %s is missing from the manifest table; add a row in docs/DECISION_JOURNAL.md", e.Name())
	}
}

// TestEveryManifestRowPointsAtExistingFile asserts every [open](...)
// link target in the manifest table resolves to a real file under
// docs/decisions/.
func TestEveryManifestRowPointsAtExistingFile(t *testing.T) {
	root := repoRoot(t)
	manifestPath := filepath.Join(root, "docs", "DECISION_JOURNAL.md")

	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	manifest := string(manifestBytes)

	matches := manifestRowRe.FindAllStringSubmatch(manifest, -1)
	require.NotEmpty(t, matches, "manifest must contain at least one decision row")

	for _, m := range matches {
		filename := m[1]
		path := filepath.Join(root, "docs", "decisions", filename)
		_, err := os.Stat(path)
		assert.NoError(t, err, "manifest references %s but the file does not exist; either restore the file or remove the manifest row", filename)
	}
}

// TestEveryDecisionFileHasManifestAnchor asserts every dj-NNN file
// has a matching <a id="dj-NNN"></a> anchor in the manifest, which
// is what external cross-refs (DECISION_JOURNAL.md#dj-NNN) land on.
func TestEveryDecisionFileHasManifestAnchor(t *testing.T) {
	root := repoRoot(t)
	decisionsDir := filepath.Join(root, "docs", "decisions")
	manifestPath := filepath.Join(root, "docs", "DECISION_JOURNAL.md")

	entries, err := os.ReadDir(decisionsDir)
	require.NoError(t, err)

	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	manifest := string(manifestBytes)

	// Collect all anchor numbers present in the manifest.
	anchors := map[string]bool{}
	for _, m := range manifestAnchorRe.FindAllStringSubmatch(manifest, -1) {
		anchors[m[1]] = true
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		m := decisionFilenameRe.FindStringSubmatch(e.Name())
		require.NotNil(t, m, "filename %s does not match dj-NNN-slug.md convention", e.Name())
		num := m[1]
		assert.True(t, anchors[num],
			"per-decision file %s has no <a id=\"dj-%s\"></a> anchor in the manifest; external refs to DECISION_JOURNAL.md#dj-%s will 404", e.Name(), num, num)
	}
}

// TestDecisionFilenameConvention asserts every file under
// docs/decisions/ follows the dj-NNN-<slug>.md naming convention.
// Catches accidental files added with non-conforming names.
func TestDecisionFilenameConvention(t *testing.T) {
	root := repoRoot(t)
	decisionsDir := filepath.Join(root, "docs", "decisions")

	entries, err := os.ReadDir(decisionsDir)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("docs/decisions/ should be flat; found subdirectory %s", e.Name())
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("docs/decisions/%s is not a .md file; the directory holds per-decision markdown files only", e.Name())
			continue
		}
		assert.Regexp(t, decisionFilenameRe, e.Name(),
			"docs/decisions/%s does not match dj-NNN-slug.md convention (three-digit zero-padded number, lowercase-hyphenated slug)", e.Name())
	}
}
