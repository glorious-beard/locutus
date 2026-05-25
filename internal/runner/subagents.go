package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// extractSubagentTranscripts is the post-run pass that turns
// sdk-messages.jsonl into one human-readable transcript file per
// subagent under <sessionDir>/subagents/. Called from DispatchActivity
// after the ACP session closes.
//
// The grouping key is request_id when present, falling back to a
// monotonically-incremented synthetic id when an emitting message has
// neither request_id nor parent_tool_use_id. The transcript file is
// named <NN>-<sanitized-subagent-type>.md so listings sort by
// dispatch order.
//
// Best-effort by design: a malformed line is skipped (logged via the
// returned err only for the open/walk failure); we never let
// extraction errors prevent the rest of the run from succeeding.
// Callers log the returned err but do not unwind.
func extractSubagentTranscripts(sessionDir string) error {
	in, err := os.Open(filepath.Join(sessionDir, "sdk-messages.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			// Runtime didn't emit anything (Codex / Gemini today, or
			// claude-code with capture disabled). Silent skip.
			return nil
		}
		return fmt.Errorf("open sdk-messages.jsonl: %w", err)
	}
	defer in.Close()

	groups, dispatchOrder := groupSDKMessagesBySubagent(in)
	if len(groups) == 0 {
		return nil
	}

	outDir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir subagents: %w", err)
	}

	for i, key := range dispatchOrder {
		group := groups[key]
		filename := fmt.Sprintf("%02d-%s.md", i+1, sanitize(group.SubagentType))
		path := filepath.Join(outDir, filename)
		if err := os.WriteFile(path, []byte(renderSubagentTranscript(group)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// subagentGroup accumulates every SDK message tied to one subagent
// dispatch. SubagentType is the canonical subagent id (spec-scout,
// spec-decision-elaborator, etc.) as reported on the dispatching
// assistant message; TaskDescription is the human-authored prompt the
// orchestrator gave the subagent. Messages preserve their wire form so
// renderSubagentTranscript can decide per-type how to present.
type subagentGroup struct {
	Key             string
	SubagentType    string
	TaskDescription string
	Messages        []json.RawMessage
}

// groupSDKMessagesBySubagent scans the JSONL stream and partitions
// messages by their request_id (or synthetic fallback when absent).
// Returns the group map + the dispatch order (group keys in the
// order they first appeared) so callers can produce stable file
// numbering across runs.
//
// SDK message shape per claude-agent-sdk 0.3.142+:
//
//	{type, subtype?, origin?, subagent_type?, request_id?, task_description?, ...}
//
// We treat any message carrying a `subagent_type` (or whose origin
// indicates task-notification) as belonging to a subagent group;
// orchestrator-level messages without those fields are skipped in
// this view (the orchestrator transcript lives in events.jsonl +
// output.md).
func groupSDKMessagesBySubagent(r io.Reader) (map[string]*subagentGroup, []string) {
	groups := map[string]*subagentGroup{}
	var order []string

	syntheticCounter := 0
	scanner := bufio.NewScanner(r)
	// SDK messages can carry full assistant-text blocks; the default
	// 64KB token cap on bufio.Scanner is too small. 4MB matches the
	// claude-agent-acp upper bound for a single JSON-RPC frame.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var meta struct {
			Type            string          `json:"type"`
			Subtype         string          `json:"subtype,omitempty"`
			Origin          json.RawMessage `json:"origin,omitempty"`
			SubagentType    string          `json:"subagent_type,omitempty"`
			RequestID       string          `json:"request_id,omitempty"`
			TaskDescription string          `json:"task_description,omitempty"`
			ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
		}
		if err := json.Unmarshal(line, &meta); err != nil {
			continue
		}

		// Skip messages that aren't subagent-scoped. The orchestrator's
		// own user/assistant messages have no subagent_type and (by
		// claude-agent-acp convention) a null parent_tool_use_id.
		if meta.SubagentType == "" && meta.ParentToolUseID == "" {
			continue
		}

		key := meta.RequestID
		if key == "" {
			key = meta.ParentToolUseID
		}
		if key == "" {
			syntheticCounter++
			key = fmt.Sprintf("synthetic-%d", syntheticCounter)
		}

		g, exists := groups[key]
		if !exists {
			g = &subagentGroup{
				Key:             key,
				SubagentType:    meta.SubagentType,
				TaskDescription: meta.TaskDescription,
			}
			groups[key] = g
			order = append(order, key)
		}
		// Some sub-messages omit subagent_type but inherit from the
		// dispatching call; preserve the first non-empty value.
		if g.SubagentType == "" && meta.SubagentType != "" {
			g.SubagentType = meta.SubagentType
		}
		if g.TaskDescription == "" && meta.TaskDescription != "" {
			g.TaskDescription = meta.TaskDescription
		}
		// Copy the line; scanner reuses its buffer.
		rawCopy := make([]byte, len(line))
		copy(rawCopy, line)
		g.Messages = append(g.Messages, rawCopy)
	}
	return groups, order
}

// renderSubagentTranscript formats one group as a single markdown
// document. The header carries the subagent type + task description
// for at-a-glance scanning; the body interleaves user / assistant /
// tool-use / result messages chronologically.
//
// Format favours readability over machine consumption — the raw
// sdk-messages.jsonl is still available alongside for programmatic
// access. Sections are headed by message type so an operator can grep
// `^## ` to navigate.
func renderSubagentTranscript(g *subagentGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Subagent transcript: %s\n\n", emptyOr(g.SubagentType, "<unknown>"))
	if g.TaskDescription != "" {
		fmt.Fprintf(&b, "**Task:** %s\n\n", g.TaskDescription)
	}
	fmt.Fprintf(&b, "**Group key:** `%s`  ·  **Messages:** %d\n\n---\n\n", g.Key, len(g.Messages))

	for i, raw := range g.Messages {
		var meta struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype,omitempty"`
			Content json.RawMessage `json:"content,omitempty"`
			Message json.RawMessage `json:"message,omitempty"`
		}
		_ = json.Unmarshal(raw, &meta)
		header := meta.Type
		if meta.Subtype != "" {
			header = fmt.Sprintf("%s · %s", meta.Type, meta.Subtype)
		}
		fmt.Fprintf(&b, "## %02d. %s\n\n", i+1, header)
		fmt.Fprintf(&b, "```json\n%s\n```\n\n", prettyJSON(raw))
	}
	return b.String()
}

// prettyJSON re-indents the input so transcript code blocks read
// without horizontal scrolling. Returns the original bytes on parse
// failure — we never want a malformed message to break rendering.
func prettyJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(pretty)
}

// emptyOr returns fallback when s is empty; convenience for headers
// where a missing field would render as an unsightly blank.
func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// sanitize maps an arbitrary string to a filename-safe form: keep
// [a-zA-Z0-9_-], collapse anything else to "-". Empty input becomes
// "unknown" so we never produce a file named "00-.md".
var sanitizePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	out := sanitizePattern.ReplaceAllString(s, "-")
	out = strings.Trim(out, "-")
	if out == "" {
		return "unknown"
	}
	return out
}

// ensureSortedOrder is a defensive helper for tests that want
// deterministic group ordering when sdk-messages arrive in
// non-monotonic order. Production callers use the natural arrival
// order, which is monotonic for a single ACP session.
func ensureSortedOrder(order []string) []string {
	sorted := append([]string(nil), order...)
	sort.Strings(sorted)
	return sorted
}
