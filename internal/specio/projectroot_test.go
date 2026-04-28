package specio_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindProjectRoot_FindsMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".borg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/manifest.json"), []byte("{}"), 0o644))

	got, err := specio.FindProjectRoot(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}

func TestFindProjectRoot_WalksUp(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".borg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/manifest.json"), []byte("{}"), 0o644))

	got, err := specio.FindProjectRoot(subdir)
	require.NoError(t, err)
	assert.Equal(t, dir, got, "should walk up to find the marker in an ancestor")
}

func TestFindProjectRoot_NotFound(t *testing.T) {
	dir := t.TempDir()
	// No marker anywhere — walk should hit FS root and error.
	_, err := specio.FindProjectRoot(dir)
	require.Error(t, err)
	assert.True(t, errors.Is(err, specio.ErrNotInProject))
}

func TestFindProjectRoot_RelativePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".borg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/manifest.json"), []byte("{}"), 0o644))

	t.Chdir(dir)
	got, err := specio.FindProjectRoot(".")
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}
