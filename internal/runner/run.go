// Package runner composes the activity registry, ACP dispatch, and
// session recording into the single high-level call CLI verbs use
// when they migrate to the DJ-135 phase 5 ACP-driven model. The
// package lives one level above internal/dispatch and
// internal/activity so it can import both without inverting the
// dispatch → acp → activity dependency tree.
//
// The composition for DispatchActivity is:
//
//	resolve runtime via activity.Registry  →
//	acp.Open subprocess for that runtime    →
//	NewSession in the project cwd           →
//	Prompt with the playbook body verbatim  →
//	stream events to the caller's writer    →
//	record events under .locutus/sessions/  →
//	return when the stream closes.
//
// Per DJ-135 phase 5 Q2 (a), the playbook body lands as the initial
// user message. Per Q3 (a), the CLI blocks and streams ACP output
// to its terminal until the session closes. Per Q6, every event the
// dispatcher observes lands under .locutus/sessions/<date>/<time>/<sid>/
// — events.jsonl carries the full event stream; tools.jsonl carries
// the distilled tool-call ↔ result pairs; playbook.md carries the
// initial prompt; output.md carries the final agent text.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/dispatch"
	"github.com/glorious-beard/locutus/internal/dispatch/acp"
	"github.com/glorious-beard/locutus/internal/dispatch/policy"
)

// ActivityRun captures the metadata of one dispatched activity run:
// the runtime selected, the session id assigned by the runtime, the
// session directory under .locutus/sessions/, and the final agent
// text output.
type ActivityRun struct {
	Runtime    string
	SessionID  string
	SessionDir string // .locutus/sessions/<date>/<time>/<sid>/
	FinalText  string
}

// DispatchActivity composes runtime resolution + ACP spawn + prompt
// + event streaming + session recording into one call. Returns when
// the ACP stream closes (clean session end), the context cancels,
// or a permanent error occurs.
//
// out is where the agent's free-text output is mirrored. progress
// is where the dispatcher writes per-tool-call status lines and
// error notices — separate writers so callers can route final text
// to stdout (for piping) while keeping live status on stderr.
// io.Discard works for either when the caller doesn't want one.
// The full structured event stream is captured under the session
// directory regardless of what the writers do.
//
// Per Q3 (a) of DJ-135 phase 5, the call blocks until the ACP
// stream closes; the caller is expected to be a CLI verb that owns
// the terminal until then.
func DispatchActivity(
	ctx context.Context,
	projectRoot string,
	activityName string,
	playbookBody string,
	reg *activity.Registry,
	out io.Writer,
	progress io.Writer,
) (*ActivityRun, error) {
	if reg == nil {
		return nil, fmt.Errorf("dispatch: registry is required")
	}
	if out == nil {
		out = io.Discard
	}
	if progress == nil {
		progress = io.Discard
	}

	runtime, err := reg.Resolve(activityName, nil /* exec.LookPath */)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}
	spawn, ok := acp.AgentSpawns[runtime]
	if !ok {
		return nil, fmt.Errorf("dispatch: runtime %q resolved but has no spawn descriptor", runtime)
	}

	sessionDir, err := makeSessionDir(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("dispatch: prepare session dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "playbook.md"), []byte(playbookBody), 0o644); err != nil {
		return nil, fmt.Errorf("dispatch: write playbook.md: %w", err)
	}

	conn, err := acp.Open(ctx, spawn, sessionDir)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open acp connection: %w", err)
	}
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("dispatch: new session: %w", err)
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	toolsPath := filepath.Join(sessionDir, "tools.jsonl")
	eventsFile, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open events log: %w", err)
	}
	defer eventsFile.Close()
	toolsFile, err := os.OpenFile(toolsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open tools log: %w", err)
	}
	defer toolsFile.Close()

	ch, err := conn.Prompt(ctx, sessionID, playbookBody, policy.AllowOncePolicy{})
	if err != nil {
		return nil, fmt.Errorf("dispatch: prompt: %w", err)
	}

	// Tool-call counter for the progress prefix. Cheap concurrency
	// guard: the ACP channel is single-reader (this goroutine), so
	// no synchronization needed.
	toolN := 0
	var finalText strings.Builder
	for ev := range ch {
		if err := writeJSONLine(eventsFile, ev); err != nil {
			return nil, fmt.Errorf("dispatch: append events log: %w", err)
		}
		switch ev.Kind {
		case dispatch.EventText, dispatch.EventResult:
			if ev.Text != "" {
				fmt.Fprint(out, ev.Text)
				finalText.WriteString(ev.Text)
			}
		case dispatch.EventToolCall:
			toolN++
			fmt.Fprintf(progress, "  %3d → %s\n", toolN, toolProgressLine(ev))
			if err := writeJSONLine(toolsFile, ev); err != nil {
				return nil, fmt.Errorf("dispatch: append tools log: %w", err)
			}
		case dispatch.EventToolResult:
			if err := writeJSONLine(toolsFile, ev); err != nil {
				return nil, fmt.Errorf("dispatch: append tools log: %w", err)
			}
		case dispatch.EventError:
			if ev.Text != "" {
				fmt.Fprintf(progress, "  [error] %s\n", ev.Text)
			}
		}
	}

	finalString := finalText.String()
	if err := os.WriteFile(filepath.Join(sessionDir, "output.md"), []byte(finalString), 0o644); err != nil {
		return nil, fmt.Errorf("dispatch: write output.md: %w", err)
	}

	return &ActivityRun{
		Runtime:    runtime,
		SessionID:  sessionID,
		SessionDir: sessionDir,
		FinalText:  finalString,
	}, nil
}

// makeSessionDir creates .locutus/sessions/<date>/<time>/<sid>/ and
// returns the absolute path. <sid> is a 6-character timestamp suffix
// — collisions are vanishingly unlikely at human cadence (one CLI
// invocation per second is fast for an interactive workflow); if we
// ever hit one, the second run errors at MkdirAll and we add a
// counter suffix then. Premature complexity otherwise.
func makeSessionDir(projectRoot string) (string, error) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	timeOfDay := now.Format("1504")
	sid := now.Format("050000")[:6]
	dir := filepath.Join(projectRoot, ".locutus", "sessions", date, timeOfDay, sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// toolProgressLine renders one tool-call event as a single human-
// readable status line for the progress writer. Prefers Claude
// Code's _meta.claudeCode.toolName when present (so an MCP tool
// surfaces as "mcp__locutus__spec_propose_decision" rather than the
// generic "ToolCall" title), with a one-key argument hint when the
// tool's input carries an obvious primary parameter (id, path,
// query, command).
//
// Falls back to the event's ToolName when no Claude-specific
// metadata is present — other runtimes (Codex / Gemini) populate
// the event differently and the dispatcher's event translator
// already normalizes them onto ToolName.
func toolProgressLine(ev dispatch.AgentEvent) string {
	name := ev.ToolName
	if raw, ok := ev.Raw, true; ok && len(raw) > 0 {
		// Best-effort _meta.claudeCode.toolName extraction. Use the
		// JSON byte slice directly rather than re-decoding the whole
		// notification — every other Raw access in the codebase pays
		// the decode cost twice; this one helper doesn't need to.
		if extracted := claudeCodeToolName(raw); extracted != "" {
			name = extracted
		}
	}
	if name == "" {
		name = "tool"
	}
	if hint := primaryInputHint(ev.ToolInput); hint != "" {
		return fmt.Sprintf("%s %s", name, hint)
	}
	return name
}

// claudeCodeToolName scans an ACP SessionNotification's JSON for
// the _meta.claudeCode.toolName field. Returns the value when
// present and a string, empty otherwise. Hand-rolled string scan
// rather than full JSON decode — this runs once per tool event in
// the progress hot path and the field's location is stable per the
// Claude Agent ACP protocol's _meta convention.
func claudeCodeToolName(raw []byte) string {
	const needle = `"toolName":"`
	i := indexOfBytes(raw, []byte(needle))
	if i < 0 {
		return ""
	}
	start := i + len(needle)
	end := start
	for end < len(raw) && raw[end] != '"' {
		end++
	}
	if end <= start || end >= len(raw) {
		return ""
	}
	return string(raw[start:end])
}

// indexOfBytes is bytes.Index re-exported under a name that doesn't
// require an extra import just for one line.
func indexOfBytes(haystack, needle []byte) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	limit := len(haystack) - n
	for i := 0; i <= limit; i++ {
		match := true
		for j := 0; j < n; j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// primaryInputHint surfaces a one-key argument summary from a tool
// invocation's input map. Priority order picks the field most
// likely to identify what the call is operating on: id (spec
// nodes), file_path (file ops), command (shell), query (search),
// pattern (grep). Returns "" when no recognized key is present.
func primaryInputHint(input map[string]any) string {
	for _, key := range []string{"id", "file_path", "path", "command", "query", "pattern"} {
		if v, ok := input[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return fmt.Sprintf("%s=%s", key, s)
		}
	}
	return ""
}

// writeJSONLine appends one event as a JSON-encoded line. Used for
// both events.jsonl and tools.jsonl so downstream archivers can tail
// either file with the same parser.
func writeJSONLine(w io.Writer, ev dispatch.AgentEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
