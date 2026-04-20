package state

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned when a state entry does not exist.
var ErrNotFound = errors.New("state: not found")

// FileStateStore persists ReconciliationState as YAML files under a base directory.
// Filenames are <approach-id>.yaml — approach IDs are kebab-case slugs (DJ-070)
// and are already filesystem-safe.
type FileStateStore struct {
	fsys    specio.FS
	baseDir string
}

// NewFileStateStore creates a store rooted at baseDir within fsys.
func NewFileStateStore(fsys specio.FS, baseDir string) *FileStateStore {
	return &FileStateStore{fsys: fsys, baseDir: baseDir}
}

func (s *FileStateStore) path(approachID string) string {
	return path.Join(s.baseDir, approachID+".yaml")
}

// Save writes state to <baseDir>/<approachID>.yaml, overwriting any existing entry.
func (s *FileStateStore) Save(rs ReconciliationState) error {
	if err := s.fsys.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("state save mkdir: %w", err)
	}
	data, err := yaml.Marshal(rs)
	if err != nil {
		return fmt.Errorf("state save marshal: %w", err)
	}
	if err := s.fsys.WriteFile(s.path(rs.ApproachID), data, 0o644); err != nil {
		return fmt.Errorf("state save write: %w", err)
	}
	return nil
}

// Load reads and unmarshals the state entry for approachID.
// Returns ErrNotFound if no entry exists.
func (s *FileStateStore) Load(approachID string) (ReconciliationState, error) {
	data, err := s.fsys.ReadFile(s.path(approachID))
	if err != nil {
		return ReconciliationState{}, ErrNotFound
	}
	var rs ReconciliationState
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return ReconciliationState{}, fmt.Errorf("state load unmarshal: %w", err)
	}
	return rs, nil
}

// Walk returns all state entries sorted by ApproachID.
func (s *FileStateStore) Walk() ([]ReconciliationState, error) {
	paths, err := s.fsys.ListDir(s.baseDir)
	if err != nil {
		// Missing directory is not an error — just an empty store.
		return nil, nil
	}

	var results []ReconciliationState
	for _, p := range paths {
		base := path.Base(p)
		if !strings.HasSuffix(base, ".yaml") {
			continue
		}
		approachID := strings.TrimSuffix(base, ".yaml")
		rs, err := s.Load(approachID)
		if err != nil {
			return nil, fmt.Errorf("state walk load %q: %w", approachID, err)
		}
		results = append(results, rs)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].ApproachID < results[j].ApproachID
	})
	return results, nil
}

// Delete removes the state entry for approachID. No-op if not found.
func (s *FileStateStore) Delete(approachID string) error {
	if err := s.fsys.Remove(s.path(approachID)); err != nil {
		return fmt.Errorf("state delete: %w", err)
	}
	return nil
}
