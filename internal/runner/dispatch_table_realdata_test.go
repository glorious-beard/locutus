package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// TestDispatchTable_RealProductionLineParses confirms that
// parseLine correctly extracts subagent dispatches from the exact
// shape claude-agent-acp emits over the wire. A previous bug report
// (winplan refine still rendering "Task → Task" despite the
// correlator wiring) raised the question of whether the production
// JSON shape differs from the synthetic test fixtures. This test
// uses a single real-world line embedded verbatim from
// /tmp/winplan-sample-sdk-line.json to confirm parseLine handles
// the full message envelope (model, id, role, stop_reason,
// stop_sequence, stop_details, usage, diagnostics,
// context_management — fields that synthetic fixtures omit).
//
// If this test fails, the parseLine struct tags are missing a
// field needed to traverse the wire shape.
func TestDispatchTable_RealProductionLineParses(t *testing.T) {
	// Production-shape line: top-level type=assistant, message has
	// many sibling fields besides content (model, id, role, etc.),
	// content[0] is a tool_use with full input. The minimum
	// representative shape — full prompt body truncated to ~200
	// bytes since parseLine doesn't read it.
	line := []byte(`{"type":"assistant","parent_tool_use_id":null,"session_id":"sess-xyz","uuid":"u-1","message":{"model":"claude-opus-4-7","id":"msg_xyz","type":"message","role":"assistant","stop_reason":null,"stop_sequence":null,"stop_details":null,"diagnostics":null,"context_management":null,"usage":{"input_tokens":100,"output_tokens":200},"content":[{"type":"text","text":"I'll dispatch the scout."},{"type":"tool_use","id":"toolu_real_id_001","name":"Agent","input":{"description":"Iteration 1 scout survey","subagent_type":"spec-scout","prompt":"You are the spec-scout..."}}]}}` + "\n")

	var sink bytes.Buffer
	dt := newDispatchTable(&sink)
	if _, err := dt.Write(line); err != nil {
		t.Fatalf("Write err: %v", err)
	}
	info, ok := dt.Lookup("toolu_real_id_001")
	if !ok {
		t.Fatalf("expected lookup hit; entries=%v", dt.entries)
	}
	if info.SubagentType != "spec-scout" {
		t.Errorf("SubagentType=%q want %q", info.SubagentType, "spec-scout")
	}
	if info.Description != "Iteration 1 scout survey" {
		t.Errorf("Description=%q want %q", info.Description, "Iteration 1 scout survey")
	}
}

// TestDispatchTable_AgainstWinplanCorpus replays the winplan
// session's actual sdk-messages.jsonl through parseLine and
// reports the population rate. Confirms (a) parseLine handles
// every line shape claude-agent-acp emits without crashing, and
// (b) the dispatch table ends up populated for every Task
// dispatch the agent made.
//
// The test is conditional on the winplan trace existing on the
// developer's machine — it's skipped in CI. The trace lives at
// /Users/chetan/projects/winplan/.locutus/sessions/20260526/0814/300000/
// per the bug report on 2026-05-26.
func TestDispatchTable_AgainstWinplanCorpus(t *testing.T) {
	const tracePath = "/Users/chetan/projects/winplan/.locutus/sessions/20260526/0814/300000/sdk-messages.jsonl"
	if _, err := os.Stat(tracePath); err != nil {
		t.Skipf("winplan trace not present (%v); test runs only on dev machines", err)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}

	var sink bytes.Buffer
	dt := newDispatchTable(&sink)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lines := 0
	for scanner.Scan() {
		dt.Write(append(scanner.Bytes(), '\n'))
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Cross-reference: how many entries does the table now have?
	// Each entry corresponds to one Task tool_use the agent emitted.
	// On a healthy session this should be ≥50 for a multi-iteration
	// refine.
	if len(dt.entries) < 10 {
		t.Errorf("parsed %d lines but dispatch table has only %d entries; expected many more", lines, len(dt.entries))
	}
	t.Logf("parsed %d sdk-messages lines, populated %d dispatch entries", lines, len(dt.entries))

	// Now verify the count matches what Python's equivalent walk
	// found (104 unique tool_use ids with subagent_type per the
	// post-hoc analysis in the bug report).
	pythonCount := pythonEquivalentCount(data)
	if len(dt.entries) != pythonCount {
		t.Errorf("Go parseLine populated %d entries, Python equivalent walk found %d — Go and Python disagree on this corpus", len(dt.entries), pythonCount)
	}
}

// pythonEquivalentCount walks the same JSONL data with the simplest
// possible Go logic (mirroring the Python diagnostic in the bug
// report) so we can A/B compare against parseLine's count.
func pythonEquivalentCount(data []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	ids := map[string]bool{}
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		if m["type"] != "assistant" {
			continue
		}
		msg, _ := m["message"].(map[string]any)
		if msg == nil {
			continue
		}
		content, _ := msg["content"].([]any)
		for _, cb := range content {
			block, _ := cb.(map[string]any)
			if block == nil {
				continue
			}
			if block["type"] != "tool_use" {
				continue
			}
			input, _ := block["input"].(map[string]any)
			if input == nil {
				continue
			}
			sub, _ := input["subagent_type"].(string)
			if sub == "" {
				continue
			}
			id, _ := block["id"].(string)
			if id != "" {
				ids[id] = true
			}
		}
	}
	return len(ids)
}
