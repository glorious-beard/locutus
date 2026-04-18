package drivers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/dispatch"
)

// ParseStream returns a streaming parser for Claude Code's NDJSON output
// emitted by `claude --print --output-format stream-json --verbose
// --include-partial-messages`.
func (d ClaudeCodeDriver) ParseStream(r io.Reader) dispatch.StreamParser {
	scanner := bufio.NewScanner(r)
	// Claude Code can emit long init/result events; default 64KB is too small.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return &claudeStreamParser{
		scanner: scanner,
		blocks:  make(map[int]*blockState),
	}
}

// RespondToAgent builds a new `claude --resume <sessionID>` invocation that
// continues the session with the supervisor's response text as the next user
// message. Always passes stream-json so the caller gets events of the same
// shape as the initial invocation.
func (d ClaudeCodeDriver) RespondToAgent(sessionID, response string) (*exec.Cmd, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("RespondToAgent: sessionID required")
	}
	return exec.Command(
		"claude",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--resume", sessionID,
		response,
	), nil
}

// ParseStream on CodexDriver is not yet implemented — codex driver support is
// deferred until real fixtures can be captured with OpenAI auth configured.
// Explicitly returning an error-emitting parser avoids a silent no-op.
func (d CodexDriver) ParseStream(r io.Reader) dispatch.StreamParser {
	return &notImplementedParser{provider: "codex"}
}

// RespondToAgent on CodexDriver is deferred along with ParseStream.
func (d CodexDriver) RespondToAgent(sessionID, response string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("codex RespondToAgent not yet implemented; pending fixtures + auth")
}

// Compile-time check that both drivers implement the supervisor-facing
// streaming contract.
var (
	_ dispatch.StreamingDriver = ClaudeCodeDriver{}
	_ dispatch.StreamingDriver = CodexDriver{}
)

// notImplementedParser yields a single error on first Next, then io.EOF.
type notImplementedParser struct {
	provider string
	errored  bool
}

func (n *notImplementedParser) Next(ctx context.Context) (dispatch.AgentEvent, error) {
	if n.errored {
		return dispatch.AgentEvent{}, io.EOF
	}
	n.errored = true
	return dispatch.AgentEvent{}, fmt.Errorf("%s streaming parser not implemented", n.provider)
}

func (n *notImplementedParser) Close() error { return nil }

// ---------- Claude stream parser ----------

type blockState struct {
	kind     string // "text", "tool_use", "thinking"
	toolName string
	toolID   string
	input    strings.Builder
	text     strings.Builder
}

type claudeStreamParser struct {
	scanner   *bufio.Scanner
	blocks    map[int]*blockState
	pending   []dispatch.AgentEvent
	sessionID string
	closed    bool
}

// Next returns the next fully-reassembled event, io.EOF at end of stream, or
// ctx.Err() if the context is canceled between reads. Pending events cached
// from a single source line are drained before any new lines are read.
func (p *claudeStreamParser) Next(ctx context.Context) (dispatch.AgentEvent, error) {
	for {
		if err := ctx.Err(); err != nil {
			return dispatch.AgentEvent{}, err
		}
		if len(p.pending) > 0 {
			evt := p.pending[0]
			p.pending = p.pending[1:]
			return evt, nil
		}
		if p.closed {
			return dispatch.AgentEvent{}, io.EOF
		}
		if !p.scanner.Scan() {
			if err := p.scanner.Err(); err != nil {
				return dispatch.AgentEvent{}, err
			}
			return dispatch.AgentEvent{}, io.EOF
		}
		line := p.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		events, err := p.handleLine(line)
		if err != nil {
			return dispatch.AgentEvent{}, err
		}
		p.pending = append(p.pending, events...)
	}
}

func (p *claudeStreamParser) Close() error {
	p.closed = true
	return nil
}

func (p *claudeStreamParser) handleLine(line []byte) ([]dispatch.AgentEvent, error) {
	// Copy — scanner reuses the slice across Scan() calls, and we may keep
	// references in AgentEvent.Raw for debugging.
	raw := make([]byte, len(line))
	copy(raw, line)

	var hdr struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &hdr); err != nil {
		return nil, fmt.Errorf("parse claude stream line: %w (line=%q)", err, truncateForError(raw))
	}

	switch hdr.Type {
	case "system":
		return p.handleSystem(raw, hdr.Subtype)
	case "stream_event":
		return p.handleStreamEvent(raw)
	case "user":
		return p.handleUser(raw)
	case "result":
		return p.handleResult(raw)
	case "error":
		return p.handleError(raw)
	case "assistant", "rate_limit_event":
		// Ignore: assistant full-message events duplicate what we reassemble
		// from stream_events; rate_limit_event is provider noise.
		return nil, nil
	default:
		return nil, nil
	}
}

func (p *claudeStreamParser) handleSystem(raw []byte, subtype string) ([]dispatch.AgentEvent, error) {
	if subtype != "init" {
		return nil, nil
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	p.sessionID = body.SessionID
	return []dispatch.AgentEvent{{
		Kind:      dispatch.EventInit,
		SessionID: body.SessionID,
		Timestamp: time.Now().UTC(),
		Raw:       raw,
	}}, nil
}

func (p *claudeStreamParser) handleStreamEvent(raw []byte) ([]dispatch.AgentEvent, error) {
	var wrapper struct {
		Event     json.RawMessage `json:"event"`
		SessionID string          `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	var hdr struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(wrapper.Event, &hdr); err != nil {
		return nil, err
	}

	switch hdr.Type {
	case "content_block_start":
		var body struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				ID    string `json:"id"`
				Text  string `json:"text"`
				Input map[string]any `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(wrapper.Event, &body); err != nil {
			return nil, err
		}
		bs := &blockState{
			kind:     body.ContentBlock.Type,
			toolName: body.ContentBlock.Name,
			toolID:   body.ContentBlock.ID,
		}
		if body.ContentBlock.Text != "" {
			bs.text.WriteString(body.ContentBlock.Text)
		}
		p.blocks[body.Index] = bs
		return nil, nil

	case "content_block_delta":
		var body struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(wrapper.Event, &body); err != nil {
			return nil, err
		}
		bs, ok := p.blocks[body.Index]
		if !ok {
			// Delta for an unknown block — providers sometimes start deltas
			// without an explicit start when resuming. Tolerate by dropping.
			return nil, nil
		}
		switch body.Delta.Type {
		case "text_delta":
			bs.text.WriteString(body.Delta.Text)
		case "input_json_delta":
			bs.input.WriteString(body.Delta.PartialJSON)
		case "signature_delta":
			// Thinking-block signatures — opaque, not useful for supervision.
		}
		return nil, nil

	case "content_block_stop":
		var body struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(wrapper.Event, &body); err != nil {
			return nil, err
		}
		bs, ok := p.blocks[body.Index]
		if !ok {
			return nil, nil
		}
		delete(p.blocks, body.Index)

		switch bs.kind {
		case "text":
			if bs.text.Len() == 0 {
				return nil, nil
			}
			return []dispatch.AgentEvent{{
				Kind:      dispatch.EventText,
				Text:      bs.text.String(),
				SessionID: p.sessionID,
				Timestamp: time.Now().UTC(),
			}}, nil
		case "tool_use":
			input, err := parseToolInput(bs.input.String())
			if err != nil {
				return nil, fmt.Errorf("parse tool input for %s: %w", bs.toolName, err)
			}
			return []dispatch.AgentEvent{{
				Kind:      dispatch.EventToolCall,
				ToolName:  bs.toolName,
				ToolInput: input,
				FilePaths: extractFilePaths(input),
				SessionID: p.sessionID,
				Timestamp: time.Now().UTC(),
			}}, nil
		case "thinking":
			// Drop thinking blocks from the supervision stream.
			return nil, nil
		}
		return nil, nil

	case "message_start", "message_delta", "message_stop":
		// Lifecycle events — no supervision-visible output.
		return nil, nil
	}
	return nil, nil
}

func (p *claudeStreamParser) handleUser(raw []byte) ([]dispatch.AgentEvent, error) {
	var body struct {
		Message struct {
			Content []struct {
				Type      string `json:"type"`
				Content   string `json:"content"`
				ToolUseID string `json:"tool_use_id"`
				IsError   bool   `json:"is_error"`
			} `json:"content"`
		} `json:"message"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	var events []dispatch.AgentEvent
	for _, c := range body.Message.Content {
		if c.Type != "tool_result" {
			continue
		}
		events = append(events, dispatch.AgentEvent{
			Kind:      dispatch.EventToolResult,
			Text:      c.Content,
			SessionID: body.SessionID,
			Timestamp: time.Now().UTC(),
		})
	}
	return events, nil
}

func (p *claudeStreamParser) handleResult(raw []byte) ([]dispatch.AgentEvent, error) {
	var body struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	kind := dispatch.EventResult
	if body.IsError {
		kind = dispatch.EventError
	}
	return []dispatch.AgentEvent{{
		Kind:      kind,
		Text:      body.Result,
		SessionID: body.SessionID,
		Timestamp: time.Now().UTC(),
		Raw:       raw,
	}}, nil
}

func (p *claudeStreamParser) handleError(raw []byte) ([]dispatch.AgentEvent, error) {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body) // best-effort; error event shapes vary
	return []dispatch.AgentEvent{{
		Kind:      dispatch.EventError,
		Text:      body.Message,
		Timestamp: time.Now().UTC(),
		Raw:       raw,
	}}, nil
}

func parseToolInput(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// extractFilePaths pulls file paths out of common tool inputs. Claude Code's
// built-in tools use "file_path" (Read/Edit/Write) or "path" (Glob/Grep).
// Bash and other tools have no path field and return nil.
func extractFilePaths(input map[string]any) []string {
	if input == nil {
		return nil
	}
	var paths []string
	for _, key := range []string{"file_path", "path"} {
		if v, ok := input[key].(string); ok && v != "" {
			paths = append(paths, v)
		}
	}
	return paths
}

func truncateForError(b []byte) string {
	const max = 120
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// Compile-time guard that the parser satisfies the dispatch interface.
var _ dispatch.StreamParser = (*claudeStreamParser)(nil)
