package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// StatusCmd shows spec summary.
type StatusCmd struct{}

func (c *StatusCmd) Run(cli *CLI) error {
	fsys := specio.NewOSFS(".")
	sd := GatherStatus(fsys)
	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(sd)
	}
	fmt.Print(render.StatusSummary(sd))
	return nil
}

// GatherStatus reads the spec from the FS and returns a StatusData struct
// summarizing the current state of the spec graph.
func GatherStatus(fsys specio.FS) render.StatusData {
	var sd render.StatusData

	// Check if GOALS.md exists.
	if _, err := fsys.Stat(".borg/GOALS.md"); err == nil {
		sd.GoalsPresent = true
	}

	// Count features.
	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.FeatureCount++
			}
		}
	}

	// Count decisions.
	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.DecisionCount++
			}
		}
	}

	// Count strategies.
	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.StrategyCount++
			}
		}
	}

	// Count bugs.
	if pairs, err := specio.WalkPairs[spec.Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err == nil {
				sd.BugCount++
			}
		}
	}

	return sd
}
