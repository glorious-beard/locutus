package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// DiffCmd previews blast radius of a spec change.
type DiffCmd struct {
	ID string `arg:"" help:"Feature, decision, or strategy ID."`
}

func (c *DiffCmd) Run(cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	br, err := RunDiff(fsys, c.ID)
	if err != nil {
		return err
	}

	fmt.Printf("Blast radius for %s (%s):\n", br.Root.ID, br.Root.Kind)
	if len(br.Decisions) > 0 {
		fmt.Println("  Decisions:")
		for _, d := range br.Decisions {
			fmt.Printf("    - %s\n", d.ID)
		}
	}
	if len(br.Strategies) > 0 {
		fmt.Println("  Strategies:")
		for _, s := range br.Strategies {
			fmt.Printf("    - %s\n", s.ID)
		}
	}
	if len(br.Files) > 0 {
		fmt.Println("  Files:")
		for _, f := range br.Files {
			fmt.Printf("    - %s\n", f.ID)
		}
	}
	return nil
}

// RunDiff loads the spec graph from fsys and computes the blast radius for the
// given spec node ID.
func RunDiff(fsys specio.FS, id string) (*spec.BlastRadius, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	g := spec.BuildGraph(features, bugs, decisions, strategies, traces)
	return spec.ComputeBlastRadius(g, id)
}

// collectObjects walks a spec directory and returns successfully loaded objects.
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
