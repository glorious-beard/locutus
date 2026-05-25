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

	"github.com/chetan/locutus/internal/activity"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/dispatch/acp"
	"github.com/chetan/locutus/internal/dispatch/policy"
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
// w is where the streamed agent text is mirrored (Q3 (a): the
// terminal in the CLI case; io.Discard in tests / non-interactive
// callers). The full structured event stream is captured under the
// session directory regardless.
//
// policyFn picks a policy.Policy for each tool-permission request
// the ACP agent surfaces. Pass policy.AllowAll for now — Locutus's
// MCP server is the only tool surface the agent can reach and the
// tools we expose are by-design safe to call without per-tool
// confirmation.
func DispatchActivity(
	ctx context.Context,
	projectRoot string,
	activityName string,
	playbookBody string,
	reg *activity.Registry,
	w io.Writer,
) (*ActivityRun, error) {
	if reg == nil {
		return nil, fmt.Errorf("dispatch: registry is required")
	}
	if w == nil {
		w = io.Discard
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

	var finalText strings.Builder
	for ev := range ch {
		if err := writeJSONLine(eventsFile, ev); err != nil {
			return nil, fmt.Errorf("dispatch: append events log: %w", err)
		}
		switch ev.Kind {
		case dispatch.EventText, dispatch.EventResult:
			if ev.Text != "" {
				fmt.Fprint(w, ev.Text)
				finalText.WriteString(ev.Text)
			}
		case dispatch.EventToolCall, dispatch.EventToolResult:
			if err := writeJSONLine(toolsFile, ev); err != nil {
				return nil, fmt.Errorf("dispatch: append tools log: %w", err)
			}
		case dispatch.EventError:
			if ev.Text != "" {
				fmt.Fprintf(w, "\n[error] %s\n", ev.Text)
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
