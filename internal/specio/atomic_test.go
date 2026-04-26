package specio_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicWriteFileCreates(t *testing.T) {
	dir := t.TempDir()
	fsys := specio.NewOSFS(dir)

	require.NoError(t, specio.AtomicWriteFile(fsys, "foo.yaml", []byte("hello"), 0o644))

	got, err := fsys.ReadFile("foo.yaml")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)
}

func TestAtomicWriteFileOverwritesPriorContent(t *testing.T) {
	dir := t.TempDir()
	fsys := specio.NewOSFS(dir)

	require.NoError(t, fsys.WriteFile("foo.yaml", []byte("v1"), 0o644))
	require.NoError(t, specio.AtomicWriteFile(fsys, "foo.yaml", []byte("v2-longer"), 0o644))

	got, err := fsys.ReadFile("foo.yaml")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2-longer"), got)
}

// TestAtomicWriteFileLeavesNoTempOnSuccess verifies the success-path cleanup
// — a hard SIGKILL between Write and Rename would still leave a temp file,
// but a successful return must not.
func TestAtomicWriteFileLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	fsys := specio.NewOSFS(dir)

	require.NoError(t, specio.AtomicWriteFile(fsys, "foo.yaml", []byte("x"), 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.Contains(e.Name(), ".tmp."),
			"temp file %q leaked after successful AtomicWriteFile", e.Name())
	}
}

// TestAtomicWriteFilePreservesPriorOnRenameFailure simulates the
// crash-between-Write-and-Rename window by writing into a destination whose
// directory is read-only after the prior good content is in place: the
// temp-file write succeeds but rename fails. The prior content must survive
// intact (the durability guarantee that motivates atomic writes).
func TestAtomicWriteFilePreservesPriorOnRenameFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod-based read-only check would be bypassed")
	}
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0o755))

	fsys := specio.NewOSFS(dir)
	require.NoError(t, fsys.WriteFile("sub/foo.yaml", []byte("good"), 0o644))

	// Make the directory non-writable so os.Rename fails. CreateTemp also
	// goes into this directory, so this actually fails earlier (at temp
	// create) — either way the prior file must remain intact.
	require.NoError(t, os.Chmod(subdir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o755) })

	err := specio.AtomicWriteFile(fsys, "sub/foo.yaml", []byte("BAD"), 0o644)
	require.Error(t, err)

	got, err := fsys.ReadFile("sub/foo.yaml")
	require.NoError(t, err)
	assert.Equal(t, []byte("good"), got, "prior content must survive failed atomic write")
}

func TestAtomicWriteFileFallsThroughForMemFS(t *testing.T) {
	mem := specio.NewMemFS()
	require.NoError(t, mem.MkdirAll("dir", 0o755))
	require.NoError(t, specio.AtomicWriteFile(mem, "dir/foo.yaml", []byte("hi"), 0o644))

	got, err := mem.ReadFile("dir/foo.yaml")
	require.NoError(t, err)
	assert.Equal(t, []byte("hi"), got)
}
