package cmd

import (
	"encoding/json"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// RunDiff loads the spec graph from fsys and computes the blast radius for
// the given spec node ID. This is the internal helper used by `refine
// --dry-run` and the MCP `refine` tool; the standalone `diff` command was
// folded into `refine` in Phase B.
func RunDiff(fsys specio.FS, id string) (*spec.BlastRadius, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	g := spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)
	return spec.ComputeBlastRadius(g, id)
}

// collectObjects walks a spec directory and returns successfully loaded
// objects. Errors on individual pairs are skipped; use WalkPairs directly
// for error reporting.
func collectObjects[T any](fsys specio.FS, dir string) ([]T, error) {
	results, err := specio.WalkPairs[T](fsys, dir)
	if err != nil {
		return nil, nil
	}
	var out []T
	for _, r := range results {
		if r.Err == nil {
			out = append(out, r.Object)
		}
	}
	return out, nil
}

// collectMarkdown walks a directory of .md files and returns successfully
// loaded objects. Used for Approach nodes whose canonical representation is
// YAML frontmatter + markdown body (not a JSON+MD pair).
func collectMarkdown[T any](fsys specio.FS, dir string) ([]T, error) {
	paths, err := fsys.ListDir(dir)
	if err != nil {
		return nil, nil
	}
	var out []T
	for _, p := range paths {
		if len(p) < 3 || p[len(p)-3:] != ".md" {
			continue
		}
		obj, _, err := specio.LoadMarkdown[T](fsys, p)
		if err == nil {
			out = append(out, obj)
		}
	}
	return out, nil
}
