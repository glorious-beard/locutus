package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sync"

	"gopkg.in/yaml.v3"
)

// FS is the narrow filesystem contract the file-backed store needs. Both
// specio.OSFS and specio.MemFS satisfy it. Declared locally so the session
// package doesn't depend on specio's full surface — and so adding
// AppendFile elsewhere doesn't require reshaping this contract.
type FS interface {
	ReadFile(name string) ([]byte, error)
	AppendFile(name string, data []byte) error
	MkdirAll(path string, perm os.FileMode) error
}

// FileStore persists events as a multi-document YAML stream at
// <root>/<workstream-id>/events.yaml. Strictly append-only on the
// OS-backed FS; safe under concurrent in-process appends via the
// store-level mutex. Not coordinated across processes.
type FileStore struct {
	mu   sync.Mutex
	fsys FS
	root string
}

// NewFileStore returns a file-backed Store rooted at the given directory.
// The workstream subdirectory is created lazily on first append.
func NewFileStore(fsys FS, root string) *FileStore {
	return &FileStore{fsys: fsys, root: root}
}

func (s *FileStore) eventsPath(workstreamID string) string {
	return path.Join(s.root, workstreamID, "events.yaml")
}

// Append writes one event as a YAML document (preceded by a `---\n`
// separator) to the workstream's events.yaml.
func (s *FileStore) Append(ctx context.Context, ev SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareEvent(ev)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := path.Join(s.root, prepared.WorkstreamID)
	if err := s.fsys.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("session append mkdir %q: %w", dir, err)
	}

	body, err := yaml.Marshal(prepared)
	if err != nil {
		return fmt.Errorf("session append marshal %q: %w", prepared.ID, err)
	}
	doc := make([]byte, 0, len(body)+4)
	doc = append(doc, []byte("---\n")...)
	doc = append(doc, body...)

	if err := s.fsys.AppendFile(s.eventsPath(prepared.WorkstreamID), doc); err != nil {
		return fmt.Errorf("session append write: %w", err)
	}
	return nil
}

// Read decodes events.yaml into an ordered slice of SessionEvent. A
// corrupt trailing document (partial crash tail) is logged and skipped;
// earlier documents still round-trip.
func (s *FileStore) Read(ctx context.Context, workstreamID string, opts ReadOpts) ([]SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.fsys.ReadFile(s.eventsPath(workstreamID))
	if err != nil {
		return nil, nil
	}

	var events []SessionEvent
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var ev SessionEvent
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			slog.Warn("session: skipping corrupt tail",
				"path", s.eventsPath(workstreamID),
				"err", err,
				"recovered_events", len(events),
			)
			break
		}
		if ev.ID == "" {
			continue
		}
		events = append(events, ev)
	}
	return filterByRange(events, opts), nil
}
