package specio

import (
	"errors"
	"io/fs"
	"path"
	"strings"
)

// SpecWriteCallback is invoked after every successful spec mutation
// routed through SavePair, SaveMarkdown, or RemovePair. Kind is the
// directory-segment name from the basePath (e.g. "decisions",
// "approaches") so callers can map it to spec.NodeKind without
// reflecting on the persisted struct. Deleted is true only on
// RemovePair calls.
//
// The callback runs synchronously on the write path; implementations
// should keep work bounded or dispatch it elsewhere. A nil callback
// is the no-op default and is what tests and bootstrap verbs see.
type SpecWriteCallback func(kind, id string, deleted bool)

// onSpecWrite is the process-global callback. Read on every write,
// written once at cmd init (or by tests). Not protected by a mutex —
// installs happen at process start before any concurrent writes; a
// future watcher would need its own synchronisation.
var onSpecWrite SpecWriteCallback

// SetSpecWriteCallback installs cb as the post-write notifier. Pass
// nil to disable.
func SetSpecWriteCallback(cb SpecWriteCallback) { onSpecWrite = cb }

// fireSpecWrite invokes the registered callback if any. basePath is a
// pair-style path (no extension) or a markdown path; the .md suffix
// is stripped so the id reads as "app-oauth", not "app-oauth.md".
func fireSpecWrite(basePath string, deleted bool) {
	cb := onSpecWrite
	if cb == nil {
		return
	}
	kind, id := splitKindAndID(basePath)
	if id == "" {
		return
	}
	cb(kind, id, deleted)
}

// splitKindAndID returns the second-to-last and last path segments of
// p, stripping a trailing .md from the id. Returns empty strings when
// p doesn't have at least two segments.
func splitKindAndID(p string) (kind, id string) {
	p = strings.TrimSuffix(p, ".md")
	dir, base := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || base == "" {
		return "", ""
	}
	return path.Base(dir), base
}

// RemovePair deletes both sidecar files for basePath (the .json and
// the .md) and fires the spec-write callback with deleted=true. A
// missing file is not fatal — RemovePair is called by cascade flows
// that may operate on partially-written nodes.
func RemovePair(fsys FS, basePath string) error {
	if err := fsys.Remove(basePath + ".json"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := fsys.Remove(basePath + ".md"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fireSpecWrite(basePath, true)
	return nil
}
