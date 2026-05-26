package runner

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
)

// TestDispatchTable_TeesToSinkAndExtracts confirms the Write
// implementation (a) writes verbatim bytes to the underlying sink
// AND (b) extracts the (toolUseId → subagent_type + description)
// mapping when the line is an assistant message with a Task
// tool_use block.
func TestDispatchTable_TeesToSinkAndExtracts(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_abc","name":"Agent","input":{"subagent_type":"spec-scout","description":"Iter 1 scout","prompt":"do the thing"}}]}}` + "\n")

	n, err := dt.Write(line)
	if err != nil {
		t.Fatalf("Write err: %v", err)
	}
	if n != len(line) {
		t.Fatalf("short write: got %d want %d", n, len(line))
	}
	if sink.String() != string(line) {
		t.Fatalf("sink got wrong bytes\nwant: %q\ngot:  %q", string(line), sink.String())
	}

	info, ok := dt.Lookup("toolu_abc")
	if !ok {
		t.Fatalf("Lookup miss for toolu_abc; entries=%v", dt.entries)
	}
	if info.SubagentType != "spec-scout" {
		t.Errorf("SubagentType got %q want %q", info.SubagentType, "spec-scout")
	}
	if info.Description != "Iter 1 scout" {
		t.Errorf("Description got %q want %q", info.Description, "Iter 1 scout")
	}
}

func TestDispatchTable_IgnoresNonAssistantMessages(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	// User and system messages must not pollute the dispatch table —
	// only assistant tool_use blocks declare subagent dispatches.
	for _, line := range [][]byte{
		[]byte(`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"),
		[]byte(`{"type":"system","subtype":"init"}` + "\n"),
		[]byte(`{"type":"result","subtype":"success"}` + "\n"),
	} {
		if _, err := dt.Write(line); err != nil {
			t.Fatalf("Write err: %v", err)
		}
	}
	if len(dt.entries) != 0 {
		t.Fatalf("non-assistant messages must not populate the table; got %v", dt.entries)
	}
}

func TestDispatchTable_IgnoresAssistantMessagesWithoutSubagentType(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	// An assistant message with a Bash or WebSearch tool_use (no
	// subagent_type) must NOT land in the dispatch table — only Task
	// dispatches are subagent invocations.
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"ls"}}]}}` + "\n")
	if _, err := dt.Write(line); err != nil {
		t.Fatalf("Write err: %v", err)
	}
	if _, ok := dt.Lookup("toolu_bash"); ok {
		t.Fatalf("Bash tool_use must not register as a subagent dispatch")
	}
}

func TestDispatchTable_TolerratesMalformedLines(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	// Malformed JSON: the write should still succeed (the file write
	// is the load-bearing side-effect) and the parser silently skips.
	bad := []byte("not even close to json\n")
	n, err := dt.Write(bad)
	if err != nil {
		t.Fatalf("Write err on malformed line: %v", err)
	}
	if n != len(bad) {
		t.Fatalf("short write: %d", n)
	}
	if len(dt.entries) != 0 {
		t.Fatal("malformed line must not populate table")
	}
}

// TestToolProgressLine_TaskFromDispatchTable confirms the
// orchestrator-visible end-user surface: when an ACP tool_call event
// carries title="Task" and the dispatch table has the matching
// subagent_type + description, the rendered line uses the rich form
// instead of "Task → Task".
func TestToolProgressLine_TaskFromDispatchTable(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)
	dt.entries["toolu_abc"] = dispatchInfo{
		SubagentType: "spec-candidate-survey",
		Description:  "Candidate survey: implementation-language",
	}

	rawACP, _ := json.Marshal(map[string]any{
		"sessionId": "sess-1",
		"update": map[string]any{
			"_meta": map[string]any{
				"claudeCode": map[string]any{"toolName": "Agent"},
			},
			"sessionUpdate": "tool_call",
			"title":         "Task",
			"toolCallId":    "toolu_abc",
		},
	})
	ev := dispatch.AgentEvent{
		Kind:     dispatch.EventToolCall,
		ToolName: "Task",
		Raw:      rawACP,
	}
	got := toolProgressLine(ev, dt)
	want := "spec-candidate-survey · Candidate survey: implementation-language"
	if got != want {
		t.Fatalf("toolProgressLine got %q\nwant %q", got, want)
	}
}

// TestDispatchTable_LookupOrWait_UnblocksOnLateWrite simulates the
// race fix: a lookup races a write, and the lookup blocks until the
// write lands. Confirms (a) the lookup returns the right info once
// the write happens (not the timeout-fallback), and (b) the wait
// duration is bounded by the actual write delay, not the full
// timeout.
func TestDispatchTable_LookupOrWait_UnblocksOnLateWrite(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	// Schedule a write to land 30ms into the lookup's 500ms timeout.
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_race","name":"Agent","input":{"subagent_type":"spec-scout","description":"late arrival"}}]}}` + "\n")
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = dt.Write(line)
	}()

	start := time.Now()
	info, ok := dt.LookupOrWait("toolu_race", 500*time.Millisecond)
	elapsed := time.Since(start)

	if !ok {
		t.Fatalf("LookupOrWait returned miss; want hit after late write")
	}
	if info.SubagentType != "spec-scout" {
		t.Errorf("SubagentType got %q want %q", info.SubagentType, "spec-scout")
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("LookupOrWait took %v; expected to unblock soon after the 30ms write (cond.Broadcast should fire immediately)", elapsed)
	}
}

// TestDispatchTable_LookupOrWait_TimesOutWhenWriteNeverComes confirms
// the upper bound on wait time: if the SDK side genuinely never
// arrives (non-Claude runtimes, or a buggy agent), the lookup falls
// back gracefully without hanging the progress writer.
func TestDispatchTable_LookupOrWait_TimesOutWhenWriteNeverComes(t *testing.T) {
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)

	start := time.Now()
	_, ok := dt.LookupOrWait("toolu_never_comes", 100*time.Millisecond)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("LookupOrWait returned hit; want miss after timeout")
	}
	if elapsed < 90*time.Millisecond {
		t.Errorf("LookupOrWait returned in %v; expected at least ~100ms wait", elapsed)
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("LookupOrWait took %v; expected close to the 100ms timeout", elapsed)
	}
}

func TestToolProgressLine_TaskFallbackWhenTableMisses(t *testing.T) {
	// When the dispatchTable has no entry for the toolCallId, the
	// progress line falls back to "Task" (or to the title when the
	// title carries useful info beyond the generic "Task"). Used to
	// verify the fallback path doesn't crash and degrades gracefully.
	var sink bytes.Buffer
	dt := newDispatchTable(&sink)
	rawACP, _ := json.Marshal(map[string]any{
		"sessionId": "sess-1",
		"update": map[string]any{
			"_meta": map[string]any{
				"claudeCode": map[string]any{"toolName": "Agent"},
			},
			"sessionUpdate": "tool_call",
			"title":         "Task",
			"toolCallId":    "toolu_missing",
		},
	})
	ev := dispatch.AgentEvent{Kind: dispatch.EventToolCall, ToolName: "Task", Raw: rawACP}
	got := toolProgressLine(ev, dt)
	if got != "Task" {
		t.Fatalf("missing-table fallback got %q want %q", got, "Task")
	}
}
