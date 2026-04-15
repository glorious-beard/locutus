package render

import (
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/check"
	"github.com/pterm/pterm"
)

// StatusSummary renders a summary of the spec state for the status command.
// It returns the rendered string (not printed directly) for testability.
func StatusSummary(data StatusData) string {
	var b strings.Builder

	// Goals status
	if data.GoalsPresent {
		b.WriteString("GOALS.md: present\n\n")
	} else {
		b.WriteString("GOALS.md: not found\n\n")
	}

	// Counts table
	tableData := pterm.TableData{
		{"Category", "Count"},
		{"Features", fmt.Sprintf("%d", data.FeatureCount)},
		{"Bugs", fmt.Sprintf("%d", data.BugCount)},
		{"Decisions", fmt.Sprintf("%d", data.DecisionCount)},
		{"Strategies", fmt.Sprintf("%d", data.StrategyCount)},
		{"Entities", fmt.Sprintf("%d", data.EntityCount)},
	}
	s, _ := pterm.DefaultTable.WithHasHeader().WithData(tableData).Srender()
	b.WriteString(s)
	b.WriteString("\n")

	// Orphan warning
	if data.OrphanCount > 0 {
		b.WriteString(fmt.Sprintf("Warning: %d orphan node(s) detected\n", data.OrphanCount))
	}

	return b.String()
}

// CheckResults renders the check command output.
func CheckResults(results []check.Result) string {
	var b strings.Builder

	for _, r := range results {
		b.WriteString(fmt.Sprintf("== %s ==\n", r.StrategyTitle))

		items := make([]pterm.BulletListItem, 0, len(r.Passed)+len(r.Failed))
		for _, p := range r.Passed {
			items = append(items, pterm.BulletListItem{
				Level:       0,
				Text:        p,
				Bullet:      "✔",
				BulletStyle: pterm.NewStyle(pterm.FgGreen),
			})
		}
		for _, f := range r.Failed {
			items = append(items, pterm.BulletListItem{
				Level:       0,
				Text:        fmt.Sprintf("%s — %s", f.Prerequisite, f.Err),
				Bullet:      "✘",
				BulletStyle: pterm.NewStyle(pterm.FgRed),
			})
		}

		list, _ := pterm.DefaultBulletList.WithItems(items).Srender()
		b.WriteString(list)
	}

	return b.String()
}

// Version renders version info.
func Version(version string) string {
	return fmt.Sprintf("Locutus version %s\n", version)
}
