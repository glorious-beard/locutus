package migrate

import (
	"fmt"
	"path"
	"strings"

	"github.com/glorious-beard/locutus/internal/frontmatter"
	"github.com/glorious-beard/locutus/internal/specio"
)

// SidecarCleanupResult is the per-run summary of CleanupSpecSidecars.
// Removed lists the FS-relative paths that were deleted. Total counts
// scanned files for the operator-facing summary. The struct is small
// because the cleanup is binary (remove or leave); there's no per-file
// reasoning to surface.
type SidecarCleanupResult struct {
	Removed []string
	// Scanned counts the total .md files inspected across the sidecar-
	// bearing directories. Files that didn't match the sidecar shape
	// (e.g. a hypothetical operator-authored markdown that happens to
	// live under .borg/spec/) are NOT removed; Scanned - len(Removed)
	// tells the operator how many such files exist.
	Scanned int
}

// sidecarBearingDirs lists the .borg/spec/<kind>/ subdirectories where
// SavePair historically wrote .md sidecars alongside .json bodies.
// Approaches are deliberately excluded — spec.Approach stores its
// canonical body as markdown via SaveMarkdown; those .md files are
// load-bearing, not sidecars.
var sidecarBearingDirs = []string{
	".borg/spec/decisions",
	".borg/spec/features",
	".borg/spec/strategies",
	".borg/spec/bugs",
}

// CleanupSpecSidecars walks the four sidecar-bearing directories and
// removes any .md file that (a) has a matching .json twin AND (b)
// contains only frontmatter (no body content). The body-empty check
// is the load-bearing safety: strategy .md files may carry the
// strategy's prose body (spec.Strategy has no Body field on the
// struct), so a strategy with substantive body content survives
// cleanup even when this migration runs. Decisions, features, and
// bugs — which carry their narrative on the typed struct — always
// have frontmatter-only sidecars and are removed.
//
// Files without a matching .json twin are left in place. That's
// defensive: if an operator left a hand-authored markdown under one
// of these directories, we don't want to delete their work.
//
// Idempotent: a project with no sidecars (cleanup already ran)
// returns {Removed: nil, Scanned: 0} on subsequent runs.
//
// Approaches under .borg/spec/approaches/ are NOT touched — those .md
// files are the canonical storage form for spec.Approach.
func CleanupSpecSidecars(fsys specio.FS) (SidecarCleanupResult, error) {
	var result SidecarCleanupResult

	for _, dir := range sidecarBearingDirs {
		entries, err := fsys.ListDir(dir)
		if err != nil {
			if isNotExist(err) {
				continue
			}
			return result, fmt.Errorf("sidecar cleanup: list %s: %w", dir, err)
		}

		// Build a set of basenames (sans extension) that have a .json
		// twin. ListDir returns full paths; collapse to basename for
		// the lookup set.
		jsonBases := map[string]bool{}
		for _, p := range entries {
			base := path.Base(p)
			if strings.HasSuffix(base, ".json") {
				jsonBases[strings.TrimSuffix(base, ".json")] = true
			}
		}

		for _, p := range entries {
			base := path.Base(p)
			if !strings.HasSuffix(base, ".md") {
				continue
			}
			result.Scanned++
			twin := strings.TrimSuffix(base, ".md")
			if !jsonBases[twin] {
				// .md without a .json twin — not a sidecar; leave alone.
				continue
			}

			// Body-aware check: only strip the sidecar when it's
			// frontmatter-only. A sidecar with substantive body
			// content (likely a strategy authored under the legacy
			// pre-DJ-135 path) survives so its content isn't lost.
			data, err := fsys.ReadFile(p)
			if err != nil {
				return result, fmt.Errorf("sidecar cleanup: read %s: %w", p, err)
			}
			var hdr specio.FrontmatterHeader
			body, perr := frontmatter.Parse(data, &hdr)
			if perr != nil {
				// Malformed frontmatter on a file we'd otherwise
				// remove — leave it alone and let the operator
				// inspect manually. Better than silent deletion of
				// something we can't classify.
				continue
			}
			if strings.TrimSpace(body) != "" {
				// Has substantive body. Preserve.
				continue
			}

			if err := fsys.Remove(p); err != nil && !isNotExist(err) {
				return result, fmt.Errorf("sidecar cleanup: remove %s: %w", p, err)
			}
			result.Removed = append(result.Removed, p)
		}
	}

	return result, nil
}
