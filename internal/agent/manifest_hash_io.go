package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
)

// manifestPath is the well-known location of .borg/manifest.json.
const manifestPath = ".borg/manifest.json"

// ReadManifestHash returns the persisted goal-layer-sync signal:
// (hash, syncedAt) from .borg/manifest.json. Returns ("", zero time,
// nil) when the manifest exists but the goals-md-hash fields are
// empty/absent (a legacy pre-DJ-139 project, or a never-synced new
// project). Returns ("", zero time, nil) when the manifest file
// itself doesn't exist (greenfield project). Errors propagate only
// on actually-broken inputs (malformed JSON, permissions).
//
// Caller-side semantics: an empty hash means "no previous sync — do
// the full bootstrap pass." A non-empty hash means "compare against
// hash(GOALS.md_now) and short-circuit on match."
func ReadManifestHash(fsys specio.FS) (string, time.Time, error) {
	data, err := fsys.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isMemFSNotExist(err) {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, fmt.Errorf("ReadManifestHash: read %s: %w", manifestPath, err)
	}
	var m spec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", time.Time{}, fmt.Errorf("ReadManifestHash: decode %s: %w", manifestPath, err)
	}
	return m.GoalsMdHash, m.GoalsMdSyncedAt, nil
}

// WriteManifestHash atomically updates .borg/manifest.json with the
// supplied (hash, syncedAt). The implementation is read-modify-write
// so every other manifest field (ProjectName, Version, Model,
// CreatedAt, future additions) is preserved unchanged. The
// AtomicWriteFile path used by SaveSpec ensures crash-safety; readers
// either see the pre-write state or the post-write state, never half.
//
// Empty hash is rejected — clearing the sync signal happens implicitly
// when a future write lands a fresh hash; explicitly storing "" would
// drop the audit-trail timestamp without a corresponding state change.
func WriteManifestHash(fsys specio.FS, hash string, syncedAt time.Time) error {
	if hash == "" {
		return fmt.Errorf("WriteManifestHash: hash is required")
	}
	data, err := fsys.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("WriteManifestHash: read %s: %w", manifestPath, err)
	}
	var m spec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("WriteManifestHash: decode %s: %w", manifestPath, err)
	}
	m.GoalsMdHash = hash
	m.GoalsMdSyncedAt = syncedAt
	updated, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("WriteManifestHash: encode %s: %w", manifestPath, err)
	}
	if err := specio.AtomicWriteFile(fsys, manifestPath, updated, 0o644); err != nil {
		return fmt.Errorf("WriteManifestHash: write %s: %w", manifestPath, err)
	}
	return nil
}

// isMemFSNotExist mirrors the helper in spec_store.go — MemFS uses a
// non-stdlib error sentinel that doesn't unwrap to fs.ErrNotExist on
// older Go versions. Both implementations must tolerate it.
func isMemFSNotExist(err error) bool {
	return err != nil && (err.Error() == "file does not exist" || err.Error() == "open: file does not exist")
}
