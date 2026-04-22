package cmd

import (
	"io/fs"
	"os"

	"github.com/chetan/locutus/internal/specio"
)

// readOnlyFS wraps a specio.FS and silently drops writes. Used for
// --dry-run flows where we want to run a pipeline to completion and report
// the result without mutating the real working tree.
type readOnlyFS struct {
	inner specio.FS
}

func newReadOnlyFS(inner specio.FS) specio.FS { return &readOnlyFS{inner: inner} }

func (r *readOnlyFS) Open(name string) (fs.File, error)        { return r.inner.Open(name) }
func (r *readOnlyFS) ReadFile(name string) ([]byte, error)     { return r.inner.ReadFile(name) }
func (r *readOnlyFS) Stat(name string) (os.FileInfo, error)    { return r.inner.Stat(name) }
func (r *readOnlyFS) ListDir(dir string) ([]string, error)     { return r.inner.ListDir(dir) }
func (r *readOnlyFS) ListSubdirs(dir string) ([]string, error) { return r.inner.ListSubdirs(dir) }
func (r *readOnlyFS) WriteFile(string, []byte, os.FileMode) error { return nil }
func (r *readOnlyFS) MkdirAll(string, os.FileMode) error       { return nil }
func (r *readOnlyFS) Remove(string) error                      { return nil }
