package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteReadPidFile_RoundTrip(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	assert.NoError(t, WritePidFile(projectRoot, 12345))

	pid, err := ReadPidFile(projectRoot)
	assert.NoError(t, err)
	assert.Equal(t, 12345, pid)

	// File lives under .locutus/, mode 0o600.
	info, err := os.Stat(PidPath(projectRoot))
	assert.NoError(t, err)
	if runtime.GOOS == "windows" {
		// Windows doesn't honor Unix permission bits; os.Stat returns
		// 0o666 or 0o444 based only on the read-only bit. Asserting
		// 0o600 here doesn't make sense — the round-trip of the file
		// content (the actual contract) is already verified above.
		t.Skip("file-mode bits are not a meaningful contract on Windows")
	}
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestReadPidFile_MissingIsZeroNoError(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	pid, err := ReadPidFile(projectRoot)
	assert.NoError(t, err, "missing PID file should not be an error — it's the 'no daemon tracked' case")
	assert.Equal(t, 0, pid)
}

func TestReadPidFile_MalformedSurfacesError(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	assert.NoError(t, writeFileMkdir(PidPath(projectRoot), []byte("not-a-number")))
	_, err := ReadPidFile(projectRoot)
	assert.Error(t, err, "malformed PID file should produce a parse error so operators see something is wrong")
}

func TestWritePidFile_OverwritesPriorContent(t *testing.T) {
	// A crashed daemon may have left a stale PID; a fresh boot should
	// replace it cleanly via the atomic rename, not stack on top.
	projectRoot := shortTempSocketDir(t)
	assert.NoError(t, WritePidFile(projectRoot, 111))
	assert.NoError(t, WritePidFile(projectRoot, 222))

	pid, err := ReadPidFile(projectRoot)
	assert.NoError(t, err)
	assert.Equal(t, 222, pid)
}

func TestRemovePidFile_MissingIsNoOp(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	assert.NoError(t, RemovePidFile(projectRoot))
}

func TestWritePidFile_NoLeftoverTmp(t *testing.T) {
	// The atomic-write path goes through a sibling .tmp; verify it's
	// gone after a successful write so we don't accumulate cruft.
	projectRoot := shortTempSocketDir(t)
	assert.NoError(t, WritePidFile(projectRoot, 42))
	tmpPath := PidPath(projectRoot) + ".tmp"
	_, err := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "no leftover .tmp file after successful write")
	_ = filepath.Dir // keep import linter quiet if path math evolves
}
