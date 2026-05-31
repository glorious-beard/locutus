package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireAdoptLock_FirstWinsSecondFails(t *testing.T) {
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	ok1, pid1 := acquireAdoptLock()
	require.True(t, ok1, "first acquisition should succeed")
	assert.Equal(t, fmt.Sprintf("%d", os.Getpid()), pid1)

	ok2, pid2 := acquireAdoptLock()
	assert.False(t, ok2, "second acquisition should fail while lock held by live PID")
	assert.Equal(t, fmt.Sprintf("%d", os.Getpid()), pid2)

	releaseAdoptLock()
	_, err = os.Stat(filepath.Join(tmp, ".locutus", "adopt.lock"))
	assert.True(t, os.IsNotExist(err), "lock file should be removed after release")
}

func TestAcquireAdoptLock_StalePIDReclaimed(t *testing.T) {
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// Plant a lock with an unrealistically-high PID guaranteed not to
	// be a live process — the liveness check should detect it as stale
	// and allow acquisition.
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".locutus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, ".locutus", "adopt.lock"), []byte("999999999"), 0o644))

	ok, pid := acquireAdoptLock()
	assert.True(t, ok, "stale lock should be reclaimed")
	assert.Equal(t, fmt.Sprintf("%d", os.Getpid()), pid)

	releaseAdoptLock()
}
