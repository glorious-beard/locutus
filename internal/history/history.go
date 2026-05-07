package history

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/specio"
)

// EventID composes the canonical event id used as both the JSON
// field and the on-disk filename (with `.json` appended). Format:
//
//	YYYY-MM-DDTHH-MM-SS-NNN[-target]-kind
//
// The timestamp is the prefix so a directory listing sorts
// chronologically. Hyphens replace colons in the time-of-day so the
// filename is portable across filesystems that reject `:` in names.
// target may be empty for events that aren't node-specific (e.g.
// plan-level events).
//
// NNN is a zero-padded same-second ordinal (process-local, resets
// each new second). It exists so two events fired within the same
// second disambiguate cleanly rather than silently overwriting on
// disk. Tests with no sleep between events depend on this.
//
// kind is the human-readable label (`refined`, `rolled-back`,
// `cascade`, `preflight`, etc.). Comes after the target so
// hyphenated target ids (`feat-foo-bar`) don't run into the label.
func EventID(kind, target string, ts time.Time) string {
	stamp := ts.Format("2006-01-02T15-04-05")
	ordinal := nextEventOrdinal(stamp)
	if target == "" {
		return fmt.Sprintf("%s-%03d-%s", stamp, ordinal, kind)
	}
	return fmt.Sprintf("%s-%03d-%s-%s", stamp, ordinal, target, kind)
}

var (
	eventOrdMu   sync.Mutex
	eventOrdSec  string
	eventOrdSeq  int
)

// nextEventOrdinal returns the 1-based ordinal for an event whose
// formatted second-stamp is `stamp`. Resets to 1 each new second.
// Process-local; a multi-process race would still collide, which
// the current single-process design doesn't expose.
func nextEventOrdinal(stamp string) int {
	eventOrdMu.Lock()
	defer eventOrdMu.Unlock()
	if stamp != eventOrdSec {
		eventOrdSec = stamp
		eventOrdSeq = 0
	}
	eventOrdSeq++
	return eventOrdSeq
}

// Event is a structured record of a spec change.
type Event struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Kind         string    `json:"kind"`
	TargetID     string    `json:"target_id"`
	OldValue     string    `json:"old_value,omitempty"`
	NewValue     string    `json:"new_value,omitempty"`
	Rationale    string    `json:"rationale,omitempty"`
	Alternatives []string  `json:"alternatives,omitempty"`
}

// Historian records and queries structured change events.
type Historian struct {
	fsys specio.FS
	dir  string
}

// NewHistorian creates a Historian backed by the given FS and directory.
func NewHistorian(fsys specio.FS, dir string) *Historian {
	return &Historian{fsys: fsys, dir: dir}
}

// Record persists an event as a JSON file. Filename: dir/eventID.json.
// The history directory is created on first write — matches narrative.go's
// lazy-mkdir pattern, so callers don't need to pre-stage the scaffold.
func (h *Historian) Record(event Event) error {
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal event %s: %w", event.ID, err)
	}
	if err := h.fsys.MkdirAll(h.dir, 0o755); err != nil {
		return fmt.Errorf("history mkdir %s: %w", h.dir, err)
	}
	fp := path.Join(h.dir, event.ID+".json")
	return specio.AtomicWriteFile(h.fsys, fp, data, 0o644)
}

// Events returns all recorded events, sorted by timestamp ascending.
func (h *Historian) Events() ([]Event, error) {
	files, err := listFiles(h.fsys, h.dir)
	if err != nil {
		return nil, err
	}

	var events []Event
	for _, f := range files {
		if path.Ext(f) != ".json" {
			continue
		}
		data, err := h.fsys.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read event %s: %w", f, err)
		}
		var evt Event
		if err := json.Unmarshal(data, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event %s: %w", f, err)
		}
		events = append(events, evt)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	return events, nil
}

// EventsForTarget returns events for a specific target ID, sorted by timestamp.
func (h *Historian) EventsForTarget(targetID string) ([]Event, error) {
	all, err := h.Events()
	if err != nil {
		return nil, err
	}
	var filtered []Event
	for _, e := range all {
		if e.TargetID == targetID {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// Alternatives returns all alternatives considered for a target, merged from all events.
func (h *Historian) Alternatives(targetID string) ([]string, error) {
	events, err := h.EventsForTarget(targetID)
	if err != nil {
		return nil, err
	}
	var alts []string
	for _, e := range events {
		alts = append(alts, e.Alternatives...)
	}
	return alts, nil
}

// listFiles returns all file paths in a directory (non-recursive), sorted.
func listFiles(fsys specio.FS, dir string) ([]string, error) {
	return fsys.ListDir(dir)
}
