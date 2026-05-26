package runner

import (
	"encoding/json"
	"io"
	"sync"
)

// dispatchTable is the runtime correlator between the orchestrator's
// SDK-message stream (which carries subagent dispatch metadata —
// subagent_type, description) and the orchestrator's ACP event stream
// (which carries tool_call_id but not the metadata for Task dispatches).
//
// Claude-agent-acp emits the assistant message that produced a Task
// tool_use BEFORE it emits the matching ACP tool_call notification for
// that tool_use. By tee'ing every inbound SDK message through this
// table on the way to disk, the table is populated by the time the
// progress writer needs to label the tool_call event. Empirically
// verified against a live session: the SDK assistant message arrives
// ~12+ events before its companion ACP tool_call (claude-agent-acp's
// internal pipeline emits the SDK side first).
//
// The table is written from the JSON-RPC reader goroutine (Write) and
// read from the event-loop goroutine (Lookup); the mutex covers both.
// On parse failure of an individual line the table silently skips
// rather than failing — the file write is the load-bearing side-effect,
// the lookup table is a best-effort UI augmentation.
type dispatchTable struct {
	mu      sync.RWMutex
	entries map[string]dispatchInfo
	sink    io.Writer
}

type dispatchInfo struct {
	SubagentType string
	Description  string
}

func newDispatchTable(sink io.Writer) *dispatchTable {
	return &dispatchTable{
		entries: make(map[string]dispatchInfo),
		sink:    sink,
	}
}

// Write implements io.Writer. Tee'd: bytes go to sink (the
// sdk-messages.jsonl file) AND get parsed for dispatch metadata.
// Parse failures don't fail the Write — the file write is the
// load-bearing side-effect, the metadata extraction is best-effort.
func (d *dispatchTable) Write(p []byte) (int, error) {
	n, err := d.sink.Write(p)
	if err != nil {
		return n, err
	}
	d.parseLine(p)
	return n, nil
}

// parseLine examines one SDK message line; if it's an assistant
// message containing a tool_use block with input.subagent_type,
// records the (toolUseId → subagent_type + description) mapping.
// Other lines (user/system/result messages, assistant messages
// without subagent dispatches) are ignored.
func (d *dispatchTable) parseLine(line []byte) {
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
		return
	}
	if m.Type != "assistant" {
		return
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
		d.mu.Lock()
		d.entries[cb.ID] = dispatchInfo{SubagentType: sub, Description: desc}
		d.mu.Unlock()
	}
}

// Lookup returns the dispatch info for the given tool_use_id (the
// ACP tool_call event's ToolCallId). Returns zero-value + false when
// the id isn't in the table — either because the SDK message hasn't
// arrived yet (very rare given the wire ordering) OR because the
// dispatch wasn't a subagent invocation (e.g. a Bash or WebSearch
// tool_use, which never carries subagent_type).
func (d *dispatchTable) Lookup(toolCallID string) (dispatchInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	info, ok := d.entries[toolCallID]
	return info, ok
}
