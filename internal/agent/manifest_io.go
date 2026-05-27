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

// GoalsMdHash returns the persisted goal-layer-sync signal:
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
//
// Held under the store's RLock for the duration of the manifest read
// so a concurrent UpdateGoalsMdHash never lets the caller observe a
// half-written manifest.
func (s *SpecStore) GoalsMdHash() (string, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return readManifestHashLocked(s.fsys)
}

// UpdateGoalsMdHash atomically updates .borg/manifest.json with the
// supplied (hash, syncedAt). The implementation is read-modify-write
// so every other manifest field (ProjectName, Version, Model,
// CreatedAt, future additions) is preserved unchanged. AtomicWriteFile
// ensures crash-safety; readers either see the pre-write state or the
// post-write state, never half.
//
// Held under the store's write Lock for the duration of the read-
// modify-write so concurrent goal-syncs across multiple MCP clients
// attached to the same per-project daemon cannot clobber each other's
// updates to non-hash fields. Without the mutex, a second client's
// in-flight UpdateGoalsMdHash could read the original manifest while
// the first client's write was already in flight, then overwrite the
// first client's hash with the second client's (using the first's
// view of the other fields).
//
// Empty hash is rejected — clearing the sync signal happens implicitly
// when a future write lands a fresh hash; explicitly storing "" would
// drop the audit-trail timestamp without a corresponding state change.
func (s *SpecStore) UpdateGoalsMdHash(hash string, syncedAt time.Time) error {
	if hash == "" {
		return fmt.Errorf("SpecStore.UpdateGoalsMdHash: hash is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.fsys.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("SpecStore.UpdateGoalsMdHash: read %s: %w", manifestPath, err)
	}
	var m spec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("SpecStore.UpdateGoalsMdHash: decode %s: %w", manifestPath, err)
	}
	m.GoalsMdHash = hash
	m.GoalsMdSyncedAt = syncedAt
	updated, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("SpecStore.UpdateGoalsMdHash: encode %s: %w", manifestPath, err)
	}
	if err := specio.AtomicWriteFile(s.fsys, manifestPath, updated, 0o644); err != nil {
		return fmt.Errorf("SpecStore.UpdateGoalsMdHash: write %s: %w", manifestPath, err)
	}
	return nil
}

// readManifestHashLocked is the lock-free body of GoalsMdHash —
// extracted so the public method can take the store's RLock at the
// outer boundary without rewriting the manifest-decoding logic. The
// caller is responsible for holding at least an RLock on the store
// before invoking.
func readManifestHashLocked(fsys specio.FS) (string, time.Time, error) {
	data, err := fsys.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, fmt.Errorf("readManifestHashLocked: read %s: %w", manifestPath, err)
	}
	var m spec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", time.Time{}, fmt.Errorf("readManifestHashLocked: decode %s: %w", manifestPath, err)
	}
	return m.GoalsMdHash, m.GoalsMdSyncedAt, nil
}
