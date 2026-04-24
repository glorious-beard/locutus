// Package session provides a workstream-scoped, append-only event store for
// multi-agent refine council work. Each event is a single turn in the
// session transcript — drafter proposes, challenger critiques, drafter
// revises — and every event carries enough context to be replayed back
// into an LLM provider's messages array without translation.
//
// This is a third persistence class, distinct from spec/state files
// (DJ-068, git-tracked audit state) and memory entries (DJ-077, distilled
// learned context). Session events are the **raw** trace; memory is what
// an agent **chose** to publish from that trace. Challenger and analyst
// agents need the former; downstream dispatchers typically only need the
// latter. See DJ-077 (partial revisit on 2026-04-23) for the rationale
// behind adopting ADK's event-log shape without its user-scoped session
// service runtime.
//
// Storage layout for the file-backed store: `<root>/<workstream-id>/
// events.yaml` as a multi-document YAML stream. Append is strictly
// append-only (O_APPEND + fsync on the OS-backed FS); a crash mid-append
// leaves a partial tail that the reader logs and skips. Gitignored under
// `.locutus/workstreams/` per DJ-073.
package session

import (
	"context"
	"errors"
	"time"
)

// SessionEvent is a single turn in a workstream's event log.
//
// Roles mirror the LLM provider API shape (system|user|assistant|tool) so
// events round-trip into a provider request's messages array without
// translation. AgentName identifies which Locutus agent spoke; Role
// identifies the message-kind the provider sees. The two are orthogonal
// (e.g. a challenger agent can speak with Role="user" when it's prompting
// the drafter; the same agent can emit Role="assistant" when it renders a
// verdict).
//
// ParentID is optional and points at the event a turn is directly replying
// to. Null for linear turns; populated when a challenger's critique
// targets a specific drafter turn, so branching council transcripts stay
// reconstructible.
type SessionEvent struct {
	ID           string    `yaml:"id"`
	WorkstreamID string    `yaml:"workstream_id"`
	AgentName    string    `yaml:"agent_name,omitempty"`
	Role         string    `yaml:"role"`
	Content      string    `yaml:"content"`
	Timestamp    time.Time `yaml:"timestamp"`
	ParentID     string    `yaml:"parent_id,omitempty"`
}

// ErrInvalidEvent signals a rejected Append: missing WorkstreamID, Role,
// or Content. The store does not silently drop bad events.
var ErrInvalidEvent = errors.New("session: invalid event")

// Store is the session-event store.
//
// Append writes a single event to its workstream's log. If the event has
// no ID, a fresh UUID is assigned; if Timestamp is zero, it is stamped
// with time.Now().UTC(). WorkstreamID, Role, and Content are required.
//
// Read returns events for a workstream filtered by ReadOpts, ordered by
// append sequence (oldest first). Since and Until are inclusive bounds;
// zero means unbounded. Limit caps the returned slice (0 = unlimited).
type Store interface {
	Append(ctx context.Context, ev SessionEvent) error
	Read(ctx context.Context, workstreamID string, opts ReadOpts) ([]SessionEvent, error)
}

// ReadOpts filters a Read call.
type ReadOpts struct {
	Since time.Time
	Until time.Time
	Limit int
}
