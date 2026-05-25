package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

// bootstrapTestStore creates an empty SpecStore for the bootstrap +
// stop tests. Kept in its own file so the bootstrap tests don't
// duplicate the construction shape used by server_test.go.
func bootstrapTestStore(t *testing.T) *agent.SpecStore {
	t.Helper()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	assert.NoError(t, err)
	return store
}

// shortTempSocketDir returns a temp directory rooted at /tmp with a
// path short enough to fit in a Unix socket's sun_path field
// (~104 chars on macOS, ~108 on Linux). t.TempDir() roots under
// $TMPDIR which on macOS expands to /var/folders/xx/.../T/ —
// already 50+ characters, leaving no room for a socket filename.
//
// Auto-cleaned via t.Cleanup.
func shortTempSocketDir(t *testing.T) string {
	t.Helper()
	// PID + atomic counter + nanos = unique enough; total stays under
	// 30 chars. Putting under /tmp/locutus-* keeps it shell-friendly
	// and outside any backup scope.
	id := atomic.AddUint64(&socketDirCounter, 1)
	dir := filepath.Join("/tmp", fmt.Sprintf("lt-mcp-%d-%d-%d", os.Getpid(), id, time.Now().UnixNano()%1000))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("shortTempSocketDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

var socketDirCounter uint64

// removeIfExists deletes path if it exists, swallowing not-exist
// errors. Used by the bootstrap tests to confirm a clean starting
// state.
func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// fileExists reports whether the path exists (any file type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
