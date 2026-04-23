package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HistoryCmd queries the historian's past-tense record of spec changes.
// Default mode prints events; flags switch to narrative read or narrative
// regeneration (DJ-026 layer 2).
type HistoryCmd struct {
	ID                   string `arg:"" optional:"" help:"Filter events to a specific target node ID."`
	Narrative            bool   `help:"Print the narrative summary from .borg/history/summary.md."`
	RegenerateNarrative  bool   `name:"regenerate-narrative" help:"Regenerate the narrative summary + detail files via the archivist/analyst agents (DJ-026)."`
	Force                bool   `help:"With --regenerate-narrative, bypass the change-hash debounce and rewrite even when events are unchanged."`
	Since                string `help:"Narrow --regenerate-narrative to events on or after this date (YYYY-MM-DD)."`
	Until                string `help:"Narrow --regenerate-narrative to events on or before this date (YYYY-MM-DD)."`
	MinEventsForDetail   int    `name:"min-events-for-detail" help:"Minimum events a target needs before a detail file is generated for it." default:"2"`
	Alternatives         bool   `help:"List alternatives considered for the target ID (requires <id>)."`
	Limit                int    `help:"Limit the number of events shown." default:"50"`
}


func (c *HistoryCmd) Run(cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	hist := history.NewHistorian(fsys, ".borg/history")

	if c.RegenerateNarrative {
		return c.runRegenerateNarrative(hist, cli)
	}

	if c.Narrative {
		data, err := fsys.ReadFile(".borg/history/summary.md")
		if err != nil {
			return fmt.Errorf("no narrative summary at .borg/history/summary.md — run `locutus history --regenerate-narrative` to create it")
		}
		fmt.Print(string(data))
		return nil
	}

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

	// Newest first, then limit.
	if len(events) > 1 {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
	}
	if c.Limit > 0 && len(events) > c.Limit {
		events = events[:c.Limit]
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(events)
	}

	if len(events) == 0 {
		fmt.Println("No history events recorded.")
		return nil
	}
	for _, e := range events {
		fmt.Printf("%s  %-20s  %-30s  %s\n",
			e.Timestamp.Format("2006-01-02 15:04"),
			e.Kind,
			e.TargetID,
			firstLine(e.Rationale),
		)
	}
	return nil
}

// runRegenerateNarrative runs the DJ-026 layer-2 pipeline. --since /
// --until narrow the event window; --force bypasses the change-hash
// debounce; --min-events-for-detail tunes the per-target threshold.
// Archivist and analyst are loaded as separate agent defs per DJ-036 —
// their system prompts (role, behavioural rules) live in
// `.borg/agents/{archivist,analyst}.md`, not hard-coded here.
func (c *HistoryCmd) runRegenerateNarrative(hist *history.Historian, cli *CLI) error {
	llm, err := getLLM()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	archivistRaw, err := agent.NamedAgentFn(fsys, llm, "archivist")
	if err != nil {
		return fmt.Errorf("archivist agent: %w", err)
	}
	analystRaw, err := agent.NamedAgentFn(fsys, llm, "analyst")
	if err != nil {
		return fmt.Errorf("analyst agent: %w", err)
	}

	cfg := history.NarrativeConfig{
		ArchivistFn:        history.GenerateFn(archivistRaw),
		AnalystFn:          history.GenerateFn(analystRaw),
		Force:              c.Force,
		MinEventsForDetail: c.MinEventsForDetail,
	}
	if c.Since != "" {
		t, err := parseHistoryDate(c.Since, "--since")
		if err != nil {
			return err
		}
		cfg.Since = &t
	}
	if c.Until != "" {
		t, err := parseHistoryDate(c.Until, "--until")
		if err != nil {
			return err
		}
		cfg.Until = &t
	}

	report, err := hist.GenerateNarrative(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("regenerate narrative: %w", err)
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if report.Skipped {
		fmt.Println("Narrative is up to date — no regeneration needed. Use --force to regenerate anyway.")
		return nil
	}
	fmt.Printf("Narrative regenerated: archivist calls=%d, analyst calls=%d.\n", report.ArchivistCalls, report.AnalystCalls)
	if len(report.DetailsRegenerated) > 0 {
		fmt.Printf("  Updated details: %v\n", report.DetailsRegenerated)
	}
	if len(report.DetailsSkipped) > 0 {
		fmt.Printf("  Unchanged details (skipped): %v\n", report.DetailsSkipped)
	}
	return nil
}

func parseHistoryDate(raw, flag string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %q is not a YYYY-MM-DD date", flag, raw)
	}
	return t, nil
}

// runHistoryMCP is the shared implementation used by the MCP handler. It
// renders a compact text result rather than streaming the full event set.
func runHistoryMCP(fsys specio.FS, input historyInput) (*mcp.CallToolResult, any, error) {
	if input.Narrative {
		data, err := fsys.ReadFile(".borg/history/summary.md")
		if err != nil {
			return errorResult(fmt.Sprintf("no narrative summary: %v", err)), nil, nil
		}
		return textResult(string(data)), nil, nil
	}

	hist := history.NewHistorian(fsys, ".borg/history")

	if input.Alternatives {
		if input.ID == "" {
			return errorResult("id is required when alternatives=true"), nil, nil
		}
		alts, err := hist.Alternatives(input.ID)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if len(alts) == 0 {
			return textResult(fmt.Sprintf("No alternatives recorded for %s.", input.ID)), nil, nil
		}
		out := fmt.Sprintf("Alternatives for %s:\n", input.ID)
		for _, a := range alts {
			out += "  - " + a + "\n"
		}
		return textResult(out), nil, nil
	}

	var events []history.Event
	var err error
	if input.ID != "" {
		events, err = hist.EventsForTarget(input.ID)
	} else {
		events, err = hist.Events()
	}
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}

	if len(events) == 0 {
		return textResult("No history events recorded."), nil, nil
	}

	var out string
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		out += fmt.Sprintf("%s  %-20s  %-30s  %s\n",
			e.Timestamp.Format("2006-01-02 15:04"),
			e.Kind,
			e.TargetID,
			firstLine(e.Rationale),
		)
	}
	return textResult(out), nil, nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
