package search

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/glorious-beard/locutus/internal/specio"
)

// specRoot is the directory under the project root that holds every
// spec node we index. Captured here so the fingerprint walker, the
// document builder, and the watcher (Phase 2) reference one source.
const specRoot = ".borg/spec"

// specKinds names the per-kind subdirectories the walker visits.
// Ordered so the fingerprint hash is deterministic across runs even
// before per-path sorting kicks in.
var specKinds = []string{"features", "strategies", "decisions", "bugs", "approaches"}

// Fingerprint is the value persisted alongside the on-disk index to
// decide whether a stored index is still valid for the current spec
// graph. Format: "<schema-version>:<sha256-hex>". A mismatch — for
// any reason: schema bump, new node, deleted node, modified node —
// triggers a full rebuild on Open.
//
// Hash inputs: every file path under .borg/spec/{features,strategies,
// decisions,bugs,approaches} paired with its mtime (unix nanos) and
// size. Single pass, ~5–10ms for 3000 files. Catches additions,
// deletions, and mtime-changing edits. Misses mtime-preserving edits
// (cp -p, restore-from-backup) — for those, the operator escape hatch
// is `rm -rf .locutus/spec_index/`.
type Fingerprint string

// computeFingerprint walks .borg/spec/ over fsys, hashes the (path,
// mtime, size) tuple for each spec file, and returns the schema-tagged
// fingerprint. Missing kind directories are skipped silently — a
// greenfield project legitimately has none of them.
func computeFingerprint(fsys specio.FS) (Fingerprint, error) {
	entries, err := collectFingerprintEntries(fsys)
	if err != nil {
		return "", err
	}
	sort.Strings(entries)

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte{'\n'})
	}
	return Fingerprint(fmt.Sprintf("%d:%s", SchemaVersion, hex.EncodeToString(h.Sum(nil)))), nil
}

// collectFingerprintEntries returns one "path\tmtime\tsize" string per
// spec file. Paths are relative to the project root for stability
// across machines.
func collectFingerprintEntries(fsys specio.FS) ([]string, error) {
	var entries []string
	for _, kind := range specKinds {
		dir := path.Join(specRoot, kind)
		files, err := fsys.ListDir(dir)
		if err != nil {
			// Missing kind directory is fine — happens on greenfield
			// projects. Any other error is fatal: we can't compute
			// a meaningful fingerprint without seeing the full set.
			if errorsIsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("fingerprint: list %s: %w", dir, err)
		}
		for _, f := range files {
			info, err := fsys.Stat(f)
			if err != nil {
				return nil, fmt.Errorf("fingerprint: stat %s: %w", f, err)
			}
			entries = append(entries, fmt.Sprintf("%s\t%d\t%d", f, info.ModTime().UnixNano(), info.Size()))
		}
	}
	return entries, nil
}

// errorsIsNotExist returns true for the standard fs.ErrNotExist
// sentinel that both OSFS (via os.ReadDir → *os.PathError) and MemFS
// (via explicit *fs.PathError construction) wrap consistently.
func errorsIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
