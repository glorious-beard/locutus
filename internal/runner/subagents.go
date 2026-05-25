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
// messages by the dispatching tool_use_id. Returns the group map +
// the dispatch order (group keys in the order they first appeared)
// so callers can produce stable file numbering across runs.
//
// Shape note: claude-agent-acp v0.33.1 (and the SDK it bundles)
// does NOT populate a top-level `subagent_type` on subagent
// messages. The discriminating fields live elsewhere:
//
//   - The dispatching call appears as an assistant message whose
//     content[] contains a `tool_use` block with name "Agent" (or
//     "Task" — naming varies across SDK versions) and an input map
//     carrying `subagent_type`, `description`, `prompt`.
//   - Downstream messages from inside that subagent's session carry
//     `parent_tool_use_id` matching the dispatching tool_use's id.
//
// So we do two passes:
//   - Pre-scan to build a tool_use.id → {subagent_type, description,
//     prompt} lookup from every tool_use block we see in assistant
//     messages.
//   - Group pass keys by parent_tool_use_id (or the dispatching
//     tool_use's own id when the message IS the dispatch), falling
//     back to request_id and finally a synthetic counter.
//
// The orchestrator's top-level assistant/user messages — those
// without a parent_tool_use_id AND without a dispatching tool_use
// block — stay in the orchestrator's transcript (events.jsonl +
// output.md), not in subagent transcripts.
func groupSDKMessagesBySubagent(r io.Reader) (map[string]*subagentGroup, []string) {
	lines := readJSONLines(r)

	// Pre-scan: build the dispatch-table from tool_use blocks. We
	// only need the input.subagent_type + input.description.
	type dispatchInfo struct {
		SubagentType    string
		TaskDescription string
	}
	dispatch := map[string]dispatchInfo{}
	for _, line := range lines {
		var m struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type  string                 `json:"type"`
					ID    string                 `json:"id"`
					Name  string                 `json:"name"`
					Input map[string]interface{} `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m.Type != "assistant" {
			continue
		}
		for _, cb := range m.Message.Content {
			if cb.Type != "tool_use" || cb.ID == "" || cb.Input == nil {
				continue
			}
			sub, _ := cb.Input["subagent_type"].(string)
			if sub == "" {
				continue
			}
			desc, _ := cb.Input["description"].(string)
			dispatch[cb.ID] = dispatchInfo{SubagentType: sub, TaskDescription: desc}
		}
	}

	// Group pass.
	groups := map[string]*subagentGroup{}
	var order []string
	syntheticCounter := 0

	for _, line := range lines {
		var meta struct {
			Type            string `json:"type"`
			Subtype         string `json:"subtype,omitempty"`
			RequestID       string `json:"request_id,omitempty"`
			ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
			Message         struct {
				Content []struct {
					Type  string                 `json:"type"`
					ID    string                 `json:"id"`
					Input map[string]interface{} `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &meta); err != nil {
			continue
		}

		// Pick the dispatching tool_use id for this message. Three
		// cases, in order:
		// 1) message is the dispatch itself: assistant with a tool_use
		//    whose input has subagent_type — group by that tool_use's
		//    own id so the dispatch and its downstream messages land
		//    together.
		// 2) message is a downstream subagent message: parent_tool_use_id
		//    points back to the dispatch.
		// 3) otherwise: orchestrator-level message; skip.
		var dispatchID string
		if meta.Type == "assistant" {
			for _, cb := range meta.Message.Content {
				if cb.Type == "tool_use" && cb.Input != nil {
					if _, ok := cb.Input["subagent_type"].(string); ok {
						dispatchID = cb.ID
						break
					}
				}
			}
		}
		if dispatchID == "" && meta.ParentToolUseID != "" {
			dispatchID = meta.ParentToolUseID
		}
		if dispatchID == "" {
			continue
		}

		info, hasInfo := dispatch[dispatchID]
		key := dispatchID
		if !hasInfo && meta.RequestID != "" {
			// Defensive fallback — keep the group but don't pretend
			// to know the subagent type.
			key = meta.RequestID
		}
		if key == "" {
			syntheticCounter++
			key = fmt.Sprintf("synthetic-%d", syntheticCounter)
		}

		g, exists := groups[key]
		if !exists {
			g = &subagentGroup{
				Key:             key,
				SubagentType:    info.SubagentType,
				TaskDescription: info.TaskDescription,
			}
			groups[key] = g
			order = append(order, key)
		}
		if g.SubagentType == "" && info.SubagentType != "" {
			g.SubagentType = info.SubagentType
		}
		if g.TaskDescription == "" && info.TaskDescription != "" {
			g.TaskDescription = info.TaskDescription
		}
		g.Messages = append(g.Messages, line)
	}
	return groups, order
}

// readJSONLines slurps the JSONL stream into a slice of raw lines so
// the two-pass extractor can re-iterate without rewinding. SDK
// messages can carry full assistant-text blocks; default 64KB cap
// is too small. 4MB matches the claude-agent-acp upper bound for a
// single JSON-RPC frame.
func readJSONLines(r io.Reader) [][]byte {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lines [][]byte
	for scanner.Scan() {
		line := scanner.Bytes()
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	}
	return lines
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
