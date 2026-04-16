// Package specio provides filesystem abstractions and spec-file I/O for the
// Locutus spec graph. It defines a narrow FS interface for testability, an
// OS-backed implementation, and a SpecStore that wraps an FS for higher-level
// operations.
package specio

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FS is a narrow filesystem interface for testability.
type FS interface {
	fs.FS // embed for reading (Open, etc.)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	Remove(name string) error
	Stat(name string) (os.FileInfo, error)
	ListDir(dir string) ([]string, error)
}

// OSFS implements FS using the real OS filesystem, rooted at a given base
// directory. All paths are resolved relative to the base.
type OSFS struct {
	base string
}

// NewOSFS returns an OSFS rooted at the given base directory.
func NewOSFS(base string) *OSFS {
	return &OSFS{base: base}
}

func (o *OSFS) resolve(name string) string {
	return filepath.Join(o.base, name)
}

// Open opens a file relative to the base directory.
func (o *OSFS) Open(name string) (fs.File, error) {
	return os.Open(o.resolve(name))
}

// ReadFile reads a file relative to the base directory.
func (o *OSFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(o.resolve(name))
}

// WriteFile writes data to a file relative to the base directory.
func (o *OSFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(o.resolve(name), data, perm)
}

// MkdirAll creates a directory path relative to the base directory.
func (o *OSFS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(o.resolve(path), perm)
}

// Remove removes a file relative to the base directory.
func (o *OSFS) Remove(name string) error {
	return os.Remove(o.resolve(name))
}

// Stat returns file info for a path relative to the base directory.
func (o *OSFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(o.resolve(name))
}

// ListDir returns sorted file paths under the given directory (non-recursive).
func (o *OSFS) ListDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(o.resolve(dir))
	if err != nil {
		return nil, err
	}
	var result []string
	for _, e := range entries {
		if !e.IsDir() {
			result = append(result, filepath.Join(dir, e.Name()))
		}
	}
	return result, nil
}

// Base returns the root directory of this OSFS.
func (o *OSFS) Base() string {
	return o.base
}

// SpecStore wraps an FS and provides higher-level spec operations.
type SpecStore struct {
	root string
	fsys FS
}

// NewSpecStore creates a SpecStore backed by the given FS, rooted at root.
func NewSpecStore(root string, fsys FS) *SpecStore {
	return &SpecStore{root: root, fsys: fsys}
}

// Root returns the base path of the spec store.
func (s *SpecStore) Root() string {
	return s.root
}

// FS returns the underlying filesystem.
func (s *SpecStore) FS() FS {
	return s.fsys
}
