package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/runner"
	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/glorious-beard/locutus/internal/specio"
)

// adoptLockPath returns the per-project adopt lock file location.
func adoptLockPath() string {
	return filepath.Join(".locutus", "adopt.lock")
}

// acquireAdoptLock atomically creates a lock file if absent, returning
// (true, own_pid) on success or (false, existing_pid) if another live
// adopt session is in flight.
// Caller MUST call releaseAdoptLock on exit (including panic recovery).
// Stale lock detection: if the file exists but its PID is no longer a
// live process (os.FindProcess + Signal(0) returns an error), the lock
// is treated as stale, removed, and a fresh lock is acquired.
func acquireAdoptLock() (bool, string) {
	p := adoptLockPath()
	if data, err := os.ReadFile(p); err == nil {
		existing := strings.TrimSpace(string(data))
		// Liveness check: probe the existing PID with signal 0.
		if pid, convErr := strconv.Atoi(existing); convErr == nil {
			if proc, findErr := os.FindProcess(pid); findErr == nil {
				if sigErr := proc.Signal(syscall.Signal(0)); sigErr == nil {
					// Process is live — lock is held.
					return false, existing
				}
			}
		}
		// Stale lock (process gone or unparseable PID) — remove and proceed.
		_ = os.Remove(p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, ""
	}
	pid := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(p, []byte(pid), 0o644); err != nil {
		return false, ""
	}
	return true, pid
}

// releaseAdoptLock removes the adopt lock file created by acquireAdoptLock.
func releaseAdoptLock() {
	_ = os.Remove(adoptLockPath())
}

// runActivityVerb is the shared entry point used by every Locutus
// verb that dispatches an activity playbook (refine / import / adopt
// / assimilate). It loads the canonical playbook, appends any
// per-invocation context, resolves the runtime, and streams the
// ACP session to stdout.
//
// Per DJ-135 phase 5 Q3 (a), the verb blocks until the ACP session
// closes. The session's final summary lands on stdout; the session
// directory under .locutus/sessions/ carries the full event log.
func runActivityVerb(ctx context.Context, _ *CLI, activityName, contextNote string, dryRun bool, dryRunFormat string) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}
	reg, err := activity.NewRegistry(fsys)
	if err != nil {
		return fmt.Errorf("%s: activity registry: %w", activityName, err)
	}
	// Resolve runtime up front so the overlay-aware playbook loader
	// can prefer .borg/plans/<activity>.<runtime>.md over the default
	// <activity>.md. The runtime is then passed through to the runner
	// — single resolution per dispatch.
	runtime, err := reg.Resolve(activityName, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", activityName, err)
	}
	act, ok := reg.Lookup(activityName)
	if !ok {
		// Defensive: Resolve succeeded so Lookup must too — guarded
		// against future refactors that might separate the two.
		return fmt.Errorf("%s: activity resolved but missing from registry", activityName)
	}
	// Adopt-lock guard: at most one code_adoption session per project
	// at a time (worktree conflicts are expensive to untangle).
	if activityName == "code_adoption" {
		ok, otherPID := acquireAdoptLock()
		if !ok {
			return fmt.Errorf("another adopt run is in flight (pid %s); wait for it to finish or remove %s if stale", otherPID, adoptLockPath())
		}
		defer releaseAdoptLock()
	}
	playbook, source, err := loadActivityPlaybook(fsys, activityName, runtime)
	if err != nil {
		return fmt.Errorf("%s: %w", activityName, err)
	}
	// Dry-run augments the operator-supplied contextNote with the
	// per-format closing-summary directive. The agent reads both as
	// one Run-context block; the env vars below set the daemon's
	// per-session capture mode independently.
	if dryRun {
		contextNote = appendNote(contextNote, dryRunContextNote(dryRunFormat))
	}
	prompt := playbook
	if contextNote != "" {
		prompt = playbook + "\n\n---\n\n## Run context\n\n" + contextNote + "\n"
	}
	prompt = injectMaxIterations(prompt, act.MaxIterations)
	// LOCUTUS_DRY_RUN[_FORMAT] are read by the spawned coding-agent
	// process's `locutus mcp` bridge child (Task 8) and forwarded to
	// the daemon via _meta on initialize. The runner picks them up
	// from os.Environ() when composing the spawn env, so setting them
	// here on the parent is enough — no signature change to runner.
	if dryRun {
		os.Setenv("LOCUTUS_DRY_RUN", "1")
		os.Setenv("LOCUTUS_DRY_RUN_FORMAT", dryRunFormat)
		defer os.Unsetenv("LOCUTUS_DRY_RUN")
		defer os.Unsetenv("LOCUTUS_DRY_RUN_FORMAT")
	}
	// Quick start banner so the operator sees we're going. Stderr
	// for operational messaging; stdout is reserved for the agent's
	// own text output so pipes work cleanly. The source path tells
	// the operator which playbook variant (default vs overlay) is
	// driving this run.
	fmt.Fprintf(os.Stderr, "→ dispatching %s activity (runtime=%s, playbook=%s)\n", activityName, runtime, source)
	run, err := runner.DispatchActivity(ctx, root, activityName, runtime, prompt, act.MaxIterations, os.Stdout, os.Stderr)
	if err != nil {
		return fmt.Errorf("%s: dispatch: %w", activityName, err)
	}
	fmt.Fprintf(os.Stderr, "\n→ session: %s (runtime=%s)\n", run.SessionDir, run.Runtime)
	// Post-dispatch authoritative dry-run render: read the session's
	// tools.jsonl directly, filter to mutation-family tool calls, and
	// emit the report in the requested format. Independent of the
	// agent's own narration — if the agent skipped its closing summary
	// (or hallucinated mutations it didn't actually call), the CLI
	// still surfaces what actually landed in the capture log. Missing
	// or malformed file is silent — the agent's narration stands.
	if dryRun && run != nil && run.SessionDir != "" {
		report := renderDryRunReportFromToolsJSONL(filepath.Join(run.SessionDir, "tools.jsonl"), dryRunFormat)
		if report != "" {
			fmt.Fprintln(os.Stdout, report)
		}
	}
	return nil
}

// dryRunContextNote returns the playbook contextNote to append when
// --dry-run is set. The wording is fixed per format; the agent reads
// it as part of its prompt and follows the closing-summary instruction.
// Unknown formats fall back to markdown so a typo doesn't strand the
// agent without rendering guidance.
func dryRunContextNote(format string) string {
	if format != "markdown" && format != "json" {
		format = "markdown"
	}
	base := "Dry-run mode is active. The daemon is capturing your spec_propose_* / spec_revise_* / spec_delete_* / spec_mark_approach_drifted / spec_update_goals_md_hash calls in a per-session overlay rather than persisting them, and discarding the overlay at session close. You will still see your own captured mutations in subsequent spec_list_manifest / spec_get / spec_search results so the workflow runs end-to-end against the would-be graph. After your final mutation phase, call mcp__locutus__spec_dry_run_report once with no arguments to retrieve the structured capture, then close with the report rendered as "
	switch format {
	case "json":
		return base + "JSON: emit the structured capture verbatim inside a single fenced ```json code block. No surrounding prose. The JSON shape is exactly what the tool returned — don't reformat or filter."
	default:
		return base + "markdown: walk the structured capture and produce a prose closing summary that counts the captured mutations by kind, names each by id with its title, and calls out cascade impact (which existing nodes' citations the mutations would touch). Close with a one-line outcome."
	}
}

// renderDryRunReportFromToolsJSONL is the CLI-side authoritative render
// path (headless only). After dispatch returns, the CLI reads the
// session's tools.jsonl, filters to mutation-family tool calls, and
// emits per format. This path is deterministic — independent of how
// the agent narrated its closing summary. Missing or malformed file
// returns "" so the caller falls back to the agent's own narration
// rather than surfacing a confusing CLI-side error.
func renderDryRunReportFromToolsJSONL(path string, format string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // graceful — caller falls back to agent narration
	}
	mutationFamily := map[string]struct{}{
		"spec_propose_decision":      {},
		"spec_revise_decision":       {},
		"spec_propose_feature":       {},
		"spec_revise_feature":        {},
		"spec_propose_strategy":      {},
		"spec_revise_strategy":       {},
		"spec_propose_goal":          {},
		"spec_revise_goal":           {},
		"spec_delete_goal":           {},
		"spec_propose_antigoal":      {},
		"spec_revise_antigoal":       {},
		"spec_delete_antigoal":       {},
		"spec_mark_approach_drifted": {},
		"spec_update_goals_md_hash":  {},
	}
	type toolCall struct {
		ToolName  string         `json:"ToolName"`
		ToolInput map[string]any `json:"ToolInput"`
		Kind      string         `json:"Kind"`
	}
	var entries []toolCall
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var tc toolCall
		if err := json.Unmarshal([]byte(line), &tc); err != nil {
			continue
		}
		if tc.Kind != "tool_call" {
			continue
		}
		if _, ok := mutationFamily[tc.ToolName]; !ok {
			continue
		}
		entries = append(entries, tc)
	}
	if format == "json" {
		// Emit in the same shape as spec_dry_run_report: {format, captured: [...]}
		type emitted struct {
			Tool string         `json:"tool"`
			ID   string         `json:"id,omitempty"`
			Body map[string]any `json:"body,omitempty"`
		}
		captured := make([]emitted, 0, len(entries))
		for _, e := range entries {
			id, _ := e.ToolInput["id"].(string)
			captured = append(captured, emitted{Tool: e.ToolName, ID: id, Body: e.ToolInput})
		}
		// Compact (no spaces after colons) so downstream tooling sees
		// the same byte shape spec_dry_run_report returns over MCP.
		out, _ := json.Marshal(map[string]any{"format": "json", "captured": captured})
		return string(out)
	}
	// markdown
	var b strings.Builder
	fmt.Fprintf(&b, "Dry-run captured %d mutation(s):\n\n", len(entries))
	for i, e := range entries {
		id, _ := e.ToolInput["id"].(string)
		title, _ := e.ToolInput["title"].(string)
		if title == "" {
			fmt.Fprintf(&b, "  %d. %s %s\n", i+1, e.ToolName, id)
		} else {
			fmt.Fprintf(&b, "  %d. %s %s — %s\n", i+1, e.ToolName, id, title)
		}
	}
	return b.String()
}

// appendNote joins two contextNote fragments with a blank-line
// separator. Empty inputs are skipped so callers don't have to
// guard around the dry-run augmentation.
func appendNote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n\n" + b
}

// injectMaxIterations substitutes the {{max_iterations}} token in a
// playbook body with the activity's registry cap (DJ-144 §6). Claude
// Code's dynamic-workflow playbooks contain the token so the in-
// runtime workflow can enforce the same cap the harness enforces for
// Codex/Gemini. Bodies without the token are returned unchanged, so
// this is safe to run for every runtime.
func injectMaxIterations(body string, cap int) string {
	return strings.ReplaceAll(body, "{{max_iterations}}", strconv.Itoa(cap))
}

// loadActivityPlaybook reads the playbook for activityName from
// .borg/plans/, preferring the runtime-specific overlay
// <activity>.<runtime>.md when present and falling back to
// <activity>.md. Missing default surfaces as a clean error mentioning
// the canonical path so operators can author or restore it.
//
// Returns (body, sourcePath, err). sourcePath is surfaced in the
// dispatch banner so the operator sees which file produced the body.
func loadActivityPlaybook(fsys specio.FS, activityName, runtime string) (string, string, error) {
	// ACP dispatch always uses the headless body (DJ-140); the
	// interactive variant is selected by the publisher for slash commands.
	data, source, err := scaffold.ResolvePlaybook(fsys, ".borg/plans", activityName, runtime, scaffold.ModeHeadless)
	if err != nil {
		return "", source, err
	}
	return string(data), source, nil
}
