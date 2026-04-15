package specio

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// MemFS is an in-memory filesystem for testing. It implements the FS interface.
type MemFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

// NewMemFS returns an empty in-memory filesystem.
func NewMemFS() *MemFS {
	return &MemFS{
		files: make(map[string][]byte),
		dirs:  map[string]bool{".": true},
	}
}

// Open returns an fs.File for the named file.
func (m *MemFS) Open(name string) (fs.File, error) {
	name = cleanPath(name)
	if data, ok := m.files[name]; ok {
		return &memFile{name: path.Base(name), data: bytes.NewReader(data), size: int64(len(data))}, nil
	}
	if m.dirs[name] {
		return &memFile{name: path.Base(name), isDir: true}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadFile returns the contents of the named file.
func (m *MemFS) ReadFile(name string) ([]byte, error) {
	name = cleanPath(name)
	data, ok := m.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

// WriteFile writes data to the named file, creating it if needed.
func (m *MemFS) WriteFile(name string, data []byte, _ os.FileMode) error {
	name = cleanPath(name)
	// Ensure parent directory exists.
	dir := path.Dir(name)
	if dir != "." && !m.dirs[dir] {
		return &fs.PathError{Op: "write", Path: name, Err: fs.ErrNotExist}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[name] = cp
	return nil
}

// MkdirAll creates a directory path (and all parents).
func (m *MemFS) MkdirAll(p string, _ os.FileMode) error {
	p = cleanPath(p)
	parts := strings.Split(p, "/")
	for i := range parts {
		m.dirs[strings.Join(parts[:i+1], "/")] = true
	}
	return nil
}

// Remove removes a file or empty directory.
func (m *MemFS) Remove(name string) error {
	name = cleanPath(name)
	if _, ok := m.files[name]; ok {
		delete(m.files, name)
		return nil
	}
	if m.dirs[name] {
		delete(m.dirs, name)
		return nil
	}
	return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
}

// Stat returns file info for the named path.
func (m *MemFS) Stat(name string) (os.FileInfo, error) {
	name = cleanPath(name)
	if data, ok := m.files[name]; ok {
		return &memFileInfo{name: path.Base(name), size: int64(len(data))}, nil
	}
	if m.dirs[name] {
		return &memFileInfo{name: path.Base(name), isDir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

// ListDir returns sorted file names under the given directory (non-recursive).
func (m *MemFS) ListDir(dir string) []string {
	dir = cleanPath(dir)
	var result []string
	for name := range m.files {
		if path.Dir(name) == dir {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func cleanPath(name string) string {
	name = path.Clean(name)
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "."
	}
	return name
}

// memFile implements fs.File for the in-memory filesystem.
type memFile struct {
	name  string
	data  *bytes.Reader
	size  int64
	isDir bool
}

func (f *memFile) Read(b []byte) (int, error) {
	if f.isDir {
		return 0, io.EOF
	}
	return f.data.Read(b)
}

func (f *memFile) Close() error { return nil }

func (f *memFile) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: f.name, size: f.size, isDir: f.isDir}, nil
}

// memFileInfo implements fs.FileInfo.
type memFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *memFileInfo) Name() string      { return fi.name }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() os.FileMode  { return 0o644 }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memFileInfo) IsDir() bool        { return fi.isDir }
func (fi *memFileInfo) Sys() any           { return nil }
