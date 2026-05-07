package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HistoryCmd queries the historian's past-tense record of spec changes.
// Default mode prints events; --narrative prints the narrative summary,
// auto-regenerating it when stale (DJ-103).
type HistoryCmd struct {
	ID                   string `arg:"" optional:"" help:"Filter events to a specific target node ID."`
	Narrative            bool   `help:"Print the narrative summary from .locutus/history/summary.md, regenerating it transparently when the event hash differs from the cached one."`
	RegenerateNarrative  bool   `name:"regenerate-narrative" help:"Regenerate the narrative summary + detail files explicitly (DJ-026). Most users don't need this — --narrative auto-regenerates on staleness."`
	Force                bool   `help:"Bypass the change-hash debounce and rewrite the narrative even when events are unchanged."`
	Since                string `help:"Narrow narrative regeneration to events on or after this date (YYYY-MM-DD)."`
	Until                string `help:"Narrow narrative regeneration to events on or before this date (YYYY-MM-DD)."`
	MinEventsForDetail   int    `name:"min-events-for-detail" help:"Minimum events a target needs before a detail file is generated for it." default:"2"`
	Alternatives         bool   `help:"List alternatives considered for the target ID (requires <id>)."`
	Limit                int    `help:"Limit the number of events shown." default:"50"`
}

// narrativeOutputDir is where the rendered summary + details/ live.
// Distinct from .borg/history/ (where the event JSONs are committed
// source of truth) — the narrative is a regenerable cache, gitignored
// per DJ-103.
const narrativeOutputDir = ".locutus/history"


func (c *HistoryCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}

	hist := history.NewHistorian(fsys, ".borg/history")

	if c.RegenerateNarrative {
		return c.runRegenerateNarrative(ctx, hist, cli)
	}

	if c.Narrative {
		return c.runNarrative(ctx, hist, fsys, cli)
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

// runNarrative is the user-facing read path. It checks whether the
// cached summary's hash matches the current events; on mismatch (or
// missing cache) it regenerates transparently via the archivist +
// analyst agents, prints a one-line stderr notice while doing so, and
// then prints the (refreshed) summary to stdout.
//
// When no LLM provider is configured, regeneration can't happen.
// Falls back to a mechanical timeline derived from event metadata so
// the verb still produces something useful — labelled as "stale" if
// a cache exists but doesn't match.
func (c *HistoryCmd) runNarrative(ctx context.Context, hist *history.Historian, fsys specio.FS, cli *CLI) error {
	manifestPath := narrativeOutputDir + "/summary.md"

	currentHash, err := hist.EventsHash(nil, nil)
	if err != nil {
		return fmt.Errorf("hash events: %w", err)
	}
	cachedHash := hist.CachedNarrativeHash(narrativeOutputDir)
	stale := cachedHash != currentHash

	if stale {
		if !agent.LLMAvailable() {
			fmt.Fprintln(os.Stderr, "warning: narrative cache is stale and no LLM provider is configured; falling back to mechanical timeline.")
			return printMechanicalTimeline(hist, cli)
		}
		fmt.Fprintln(os.Stderr, "Regenerating narrative (events changed since last cache)…")
		if err := c.regenerateNarrativeQuietly(ctx, hist, fsys); err != nil {
			return fmt.Errorf("auto-regenerate narrative: %w", err)
		}
	}

	data, err := fsys.ReadFile(manifestPath)
	if err != nil {
		// Either regeneration silently failed to write, or there's
		// no cache and no LLM. The latter is the mechanical-fallback
		// case; we already covered that above. The former is a real
		// error.
		return fmt.Errorf("read narrative %s: %w", manifestPath, err)
	}
	fmt.Print(string(data))
	return nil
}

// regenerateNarrativeQuietly runs the same pipeline as
// --regenerate-narrative without printing the regen-summary report.
// Used by the auto-regen path on --narrative.
func (c *HistoryCmd) regenerateNarrativeQuietly(ctx context.Context, hist *history.Historian, fsys specio.FS) error {
	llm, err := getLLM()
	if err != nil {
		return err
	}
	archivistRaw, err := scaffoldAgentFn(fsys, llm, "archivist")
	if err != nil {
		return fmt.Errorf("archivist agent: %w", err)
	}
	analystRaw, err := scaffoldAgentFn(fsys, llm, "analyst")
	if err != nil {
		return fmt.Errorf("analyst agent: %w", err)
	}
	cfg := history.NarrativeConfig{
		ArchivistFn:        history.GenerateFn(archivistRaw),
		AnalystFn:          history.GenerateFn(analystRaw),
		MinEventsForDetail: c.MinEventsForDetail,
		OutputDir:          narrativeOutputDir,
	}
	_, err = hist.GenerateNarrative(ctx, cfg)
	return err
}

// scaffoldAgentFn returns a GenerateFn-shaped closure that dispatches
// the named agent against `llm`. AgentDef is loaded via
// scaffold.LoadAgent so a per-project edit at .borg/agents/<id>.md
// wins, with the embedded scaffold copy as fallback when the project
// file is absent or stale (DJ-103). Replaces agent.NamedAgentFn for
// the history paths so a binary upgrade ships fresh archivist /
// analyst prompts without forcing the user to run `update --reset`.
func scaffoldAgentFn(fsys specio.FS, llm agent.AgentExecutor, agentID string) (func(ctx context.Context, userPrompt string) (string, error), error) {
	def, err := scaffold.LoadAgent(fsys, agentID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, user string) (string, error) {
		input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: user}}}
		resp, err := llm.Run(ctx, def, input)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}, nil
}

// printMechanicalTimeline emits a deterministic timeline from event
// metadata when the LLM is unavailable. Better than failing — the
// reader still sees what happened, even if the prose is missing.
func printMechanicalTimeline(hist *history.Historian, cli *CLI) error {
	events, err := hist.Events()
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Println("No history events recorded.")
		return nil
	}
	fmt.Println("# Project History (mechanical timeline)")
	fmt.Println()
	fmt.Printf("_Based on %d events, %s to %s. Configure an LLM provider to get the narrative summary._\n\n",
		len(events),
		events[0].Timestamp.Format("2006-01-02"),
		events[len(events)-1].Timestamp.Format("2006-01-02"))
	fmt.Println("## Timeline")
	fmt.Println()
	for _, e := range events {
		fmt.Printf("- %s — %s `%s`: %s\n",
			e.Timestamp.Format("2006-01-02"),
			e.Kind, e.TargetID,
			firstLine(e.Rationale))
	}
	return nil
}

// runRegenerateNarrative runs the DJ-026 layer-2 pipeline explicitly.
// --since / --until narrow the event window; --force bypasses the
// change-hash debounce; --min-events-for-detail tunes the per-target
// threshold. Most users won't need this — --narrative auto-regenerates
// on staleness — but it's preserved for the bounded-window and
// --force cases.
func (c *HistoryCmd) runRegenerateNarrative(ctx context.Context, hist *history.Historian, cli *CLI) error {
	llm, err := getLLM()
	if err != nil {
		return err
	}
	fsys, _, err := projectFS()
	if err != nil {
		return err
	}

	archivistRaw, err := scaffoldAgentFn(fsys, llm, "archivist")
	if err != nil {
		return fmt.Errorf("archivist agent: %w", err)
	}
	analystRaw, err := scaffoldAgentFn(fsys, llm, "analyst")
	if err != nil {
		return fmt.Errorf("analyst agent: %w", err)
	}

	cfg := history.NarrativeConfig{
		ArchivistFn:        history.GenerateFn(archivistRaw),
		AnalystFn:          history.GenerateFn(analystRaw),
		Force:              c.Force,
		MinEventsForDetail: c.MinEventsForDetail,
		OutputDir:          narrativeOutputDir,
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

	report, err := hist.GenerateNarrative(ctx, cfg)
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
		data, err := fsys.ReadFile(narrativeOutputDir + "/summary.md")
		if err != nil {
			return errorResult(fmt.Sprintf("no narrative summary at %s/summary.md — run `locutus history --narrative` to generate one", narrativeOutputDir)), nil, nil
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
