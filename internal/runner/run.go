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
	"sync"
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

	// Capture the ACP subprocess's stderr under the session directory
	// rather than leaking it to the operator's terminal. claude-agent-acp
	// emits one "No onPostToolUseHook found" warning per tool call —
	// useful for debugging the bridge, useless noise during normal
	// runs. The captured log is available under
	// .locutus/sessions/<sid>/acp-stderr.log for forensic use.
	acpStderr, err := os.OpenFile(filepath.Join(sessionDir, "acp-stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open acp stderr log: %w", err)
	}
	defer acpStderr.Close()

	conn, err := acp.Open(ctx, spawn, sessionDir, acpStderr)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open acp connection: %w", err)
	}
	defer conn.Close()

	// Opt into claude-agent-acp's raw-SDK-message feed. Default filter
	// captures the substantive content (user/assistant/result/system),
	// which is enough to reconstruct per-subagent transcripts in the
	// post-run extractor. LOCUTUS_CAPTURE_STREAM_EVENTS=1 widens the
	// filter to include the raw streaming chunks (one per token), which
	// inflates volume 10x but unlocks per-chunk latency analysis and
	// mid-stream cancellation forensics.
	//
	// Other runtimes (Codex, Gemini) ignore the unknown _meta keys per
	// the ACP extensibility contract, so it's safe to always pass the
	// option. Only claude-agent-acp emits matching notifications today.
	sessionID, err := conn.NewSessionWithOptions(ctx, projectRoot, claudeSessionOptionsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("dispatch: new session: %w", err)
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	toolsPath := filepath.Join(sessionDir, "tools.jsonl")
	sdkPath := filepath.Join(sessionDir, "sdk-messages.jsonl")
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
	// sdk-messages.jsonl receives the raw _claude/sdkMessage feed.
	// The file is always opened (one inode per session) and stays
	// empty when the runtime doesn't emit anything — keeps the
	// post-run extractor logic simple (open-or-skip rather than
	// "file may or may not exist").
	sdkFile, err := os.OpenFile(sdkPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dispatch: open sdk-messages log: %w", err)
	}
	defer sdkFile.Close()

	ch, err := conn.PromptWithSDKSink(ctx, sessionID, playbookBody, policy.AllowOncePolicy{}, sdkFile)
	if err != nil {
		return nil, fmt.Errorf("dispatch: prompt: %w", err)
	}

	// Tool-call counter for the progress prefix. Cheap concurrency
	// guard: the ACP channel is single-reader (this goroutine), so
	// no synchronization needed within the main loop, but the
	// heartbeat goroutine reads through a mutex.
	var (
		toolN     int
		runStart  = time.Now()
		heartbeat sync.Mutex
	)

	// Heartbeat goroutine: emits an elapsed-time line every 60s so
	// the operator sees progress even when the agent's subagents are
	// running grounded research without surfacing per-call events to
	// the orchestrator session. Stops when the main loop exits via
	// the done channel.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				heartbeat.Lock()
				n := toolN
				heartbeat.Unlock()
				elapsed := time.Since(runStart).Round(time.Second)
				fmt.Fprintf(progress, "  [%s] ⋯ elapsed %s · %d tool calls so far\n",
					time.Now().Format("15:04:05"), elapsed, n)
			}
		}
	}()
	defer close(done)

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
			heartbeat.Lock()
			toolN++
			n := toolN
			heartbeat.Unlock()
			fmt.Fprintf(progress, "  [%s] %3d → %s\n",
				time.Now().Format("15:04:05"), n, toolProgressLine(ev))
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

	// Best-effort post-run: re-read sdk-messages.jsonl and produce
	// one human-readable transcript per subagent under
	// <sessionDir>/subagents/. Failures here are logged but do not
	// fail the run — the raw jsonl is still available alongside.
	if err := extractSubagentTranscripts(sessionDir); err != nil {
		fmt.Fprintf(progress, "  [warn] subagent transcript extraction failed: %v\n", err)
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
// readable status line for the progress writer.
//
// Name resolution order:
//  1. Claude Code's _meta.claudeCode.toolName when present (surfaces
//     MCP tools with their mcp__locutus__-prefixed canonical name
//     rather than the generic ACP "ToolCall" title).
//  2. The dispatcher's normalized ev.ToolName (the fallback for
//     non-Claude runtimes).
//  3. "tool" as the absolute fallback.
//
// Task-tool dispatches get special handling: when the call is
// Claude Code's Task tool, the subagent type is in the input map's
// "subagent_type" field. Surface it inline so "Agent" lines read as
// "Task → spec-decision-elaborator" instead of an undifferentiated
// "Agent". Equivalent fields on other runtimes are added as they're
// empirically validated.
//
// Primary-input hint surfaces one identifying argument (id,
// file_path, command, query, pattern) so the line carries more than
// the tool name alone.
func toolProgressLine(ev dispatch.AgentEvent) string {
	// title is the ACP tool_call.Title field (set by the dispatcher's
	// event translator into ev.ToolName before we override). For Claude
	// Code's Bash/Read/Write tools it's typically the tool name; for
	// the Task tool it's the human-authored description the agent gave
	// the dispatch (e.g. "Initial survey against empty graph") which
	// is far more useful than the canonical "Agent" string.
	title := ev.ToolName
	// canonical is _meta.claudeCode.toolName when present — the
	// runtime-canonical tool id (Bash / Agent / mcp__locutus__*).
	canonical := ""
	if len(ev.Raw) > 0 {
		canonical = claudeCodeToolName(ev.Raw)
	}

	// Task tool: prefer the human-authored title; the canonical name
	// ("Agent") is generic and hides what the dispatch is for.
	if canonical == "Agent" || canonical == "Task" {
		if title != "" && title != canonical {
			return fmt.Sprintf("Task → %s", title)
		}
		return "Task"
	}

	// Everything else: prefer the canonical name (it's more
	// machine-precise — e.g. "mcp__locutus__spec_propose_decision"
	// over the title-cased "Spec Propose Decision"). Fall back to
	// title when no canonical is present (non-Claude runtimes).
	name := canonical
	if name == "" {
		name = title
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

// claudeSessionOptionsFromEnv builds the per-session ClaudeSessionOptions
// from the LOCUTUS_CAPTURE_STREAM_EVENTS env var. The default (env unset
// or "0"/"false") emits the substantive content classes — user, assistant,
// result, system — which together are enough to reconstruct per-subagent
// transcripts in the post-run extractor. Setting the env var to "1" /
// "true" widens the filter to include stream_event, which inflates volume
// 10x but unlocks per-chunk latency analysis and mid-stream cancellation
// forensics.
//
// Other runtimes (Codex, Gemini) ignore the unknown _meta keys per the
// ACP extensibility contract; passing the option unconditionally is safe.
func claudeSessionOptionsFromEnv() acp.ClaudeSessionOptions {
	base := []acp.SDKMessageFilter{
		{Type: "user"},
		{Type: "assistant"},
		{Type: "result"},
		{Type: "system"},
	}
	if envBool("LOCUTUS_CAPTURE_STREAM_EVENTS") {
		base = append(base, acp.SDKMessageFilter{Type: "stream_event"})
	}
	return acp.ClaudeSessionOptions{EmitFilter: base}
}

// envBool reads a boolean env var with the conventional 1/true/yes/on
// truthy set. Empty or unrecognized values are false. Matches the
// behavior of common Go env-var helpers without pulling in a dependency.
func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
