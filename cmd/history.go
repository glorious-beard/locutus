package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/glorious-beard/locutus/internal/history"
)

// HistoryCmd queries the historian's past-tense record of spec changes.
// Default mode prints events; --alternatives lists the alternatives
// considered for a target. The LLM-driven --narrative regeneration
// path from DJ-103 retired with the Go council in DJ-135 phase 5; a
// future narrate activity (ACP-dispatched) replaces it if there's
// demand. Until then, the mechanical event timeline is the only view.
type HistoryCmd struct {
	ID           string `arg:"" optional:"" help:"Filter events to a specific target node ID."`
	Alternatives bool   `help:"List alternatives considered for the target ID (requires <id>)."`
	Limit        int    `help:"Limit the number of events shown." default:"50"`
}

func (c *HistoryCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}

	hist := history.NewHistorian(fsys, ".borg/history")

	if c.Alternatives {
		if c.ID == "" {
			return fmt.Errorf("--alternatives requires a target node ID")
		}
		alts, err := hist.Alternatives(c.ID)
		if err != nil {
			return fmt.Errorf("alternatives for %s: %w", c.ID, err)
		}
		if cli.JSON {
			return json.NewEncoder(os.Stdout).Encode(alts)
		}
		if len(alts) == 0 {
			fmt.Printf("No alternatives recorded for %s.\n", c.ID)
			return nil
		}
		fmt.Printf("Alternatives considered for %s:\n", c.ID)
		for _, a := range alts {
			fmt.Printf("  - %s\n", a)
		}
		return nil
	}

	var events []history.Event
	if c.ID != "" {
		events, err = hist.EventsForTarget(c.ID)
	} else {
		events, err = hist.Events()
	}
	if err != nil {
		return fmt.Errorf("reading events: %w", err)
	}

	if c.Limit > 0 && len(events) > c.Limit {
		events = events[len(events)-c.Limit:]
	}
	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(events)
	}
	if len(events) == 0 {
		fmt.Println("No history events recorded.")
		return nil
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		fmt.Printf("%s  %-20s  %-30s  %s\n",
			e.Timestamp.Format("2006-01-02 15:04"),
			e.Kind, e.TargetID,
			firstLine(e.Rationale))
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
