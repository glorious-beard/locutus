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
// The wire order is not deterministic: claude-agent-acp sometimes emits
// the assistant SDK message before the ACP tool_call notification, and
// sometimes after. Prior versions of this code blocked the event loop
// waiting for the SDK side to land, which fundamentally conflicted with
// the JSON-RPC reader's serialization (a blocked event loop fills
// ap.events, which blocks SessionUpdate, which blocks the reader, which
// blocks delivery of the very SDK message the loop is waiting for).
//
// The current API breaks that knot: Subscribe is strictly non-blocking.
// When the runner sees a Task tool_call event, it Subscribes with a
// callback; if the entry already exists, the callback fires
// synchronously; otherwise it fires when parseLine lands the entry from
// a later SDK message. The event loop returns immediately either way
// and prints a bare "Task" line; the callback emits a backfill line
// when correlation arrives so the operator sees both the dispatch
// timing AND which subagent was dispatched.
//
// The table is written from the JSON-RPC reader goroutine (Write) and
// read from the event-loop goroutine (Lookup / Subscribe); the mutex
// covers both. On parse failure of an individual line the table
// silently skips rather than failing — the file write is the
// load-bearing side-effect, the lookup table is a best-effort UI
// augmentation.
type dispatchTable struct {
	mu       sync.Mutex
	entries  map[string]dispatchInfo
	pending  map[string][]func(dispatchInfo)
	sink     io.Writer
}

type dispatchInfo struct {
	SubagentType string
	Description  string
}

func newDispatchTable(sink io.Writer) *dispatchTable {
	return &dispatchTable{
		entries: make(map[string]dispatchInfo),
		pending: make(map[string][]func(dispatchInfo)),
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
		info := dispatchInfo{SubagentType: sub, Description: desc}

		// Capture pending callbacks under the lock, then invoke
		// them OUTSIDE the lock — callbacks may write to the
		// progress writer (which can block on the terminal) and
		// holding d.mu across that risks blocking the JSON-RPC
		// reader on terminal IO. Detach the slice and clear the
		// pending entry before unlocking; callers are responsible
		// for not re-entering the table from inside the callback.
		d.mu.Lock()
		d.entries[cb.ID] = info
		cbs := d.pending[cb.ID]
		delete(d.pending, cb.ID)
		d.mu.Unlock()

		for _, fn := range cbs {
			fn(info)
		}
	}
}

// Lookup returns the dispatch info for the given tool_use_id (the
// ACP tool_call event's ToolCallId). Returns zero-value + false when
// the id isn't in the table — either because the SDK message hasn't
// arrived yet OR because the dispatch wasn't a subagent invocation
// (e.g. a Bash or WebSearch tool_use, which never carries
// subagent_type). Non-blocking.
func (d *dispatchTable) Lookup(toolCallID string) (dispatchInfo, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	info, ok := d.entries[toolCallID]
	return info, ok
}

// Subscribe registers a callback to fire when the dispatch info for
// toolCallID becomes available. If the entry already exists, the
// callback fires synchronously before Subscribe returns; otherwise it
// fires from parseLine (the JSON-RPC reader goroutine) when the
// matching SDK message lands.
//
// Non-blocking by design: the previous LookupOrWait API stalled the
// runner's event loop, which back-pressured ap.events and ultimately
// blocked the JSON-RPC reader from delivering the very SDK message
// the loop was waiting for. Subscribe inverts the control flow — the
// caller returns immediately and the late arrival emits a backfill
// line via the callback.
//
// Callbacks run holding no locks. They MUST NOT call back into the
// dispatchTable (no Subscribe / Lookup recursion). Cheap callbacks
// (a fmt.Fprintf to a writer) are fine; long-running work should be
// pushed to a goroutine inside the callback.
func (d *dispatchTable) Subscribe(toolCallID string, cb func(dispatchInfo)) {
	d.mu.Lock()
	if info, ok := d.entries[toolCallID]; ok {
		d.mu.Unlock()
		cb(info)
		return
	}
	d.pending[toolCallID] = append(d.pending[toolCallID], cb)
	d.mu.Unlock()
}
