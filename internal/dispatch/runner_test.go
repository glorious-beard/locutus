package dispatch

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionRunner_StreamsStdout(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `printf "a\nb\n"`)
	rc, err := ProductionRunner(cmd)
	require.NoError(t, err)

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(out))

	assert.NoError(t, rc.Close(), "Close after clean exit should not error")
}

func TestProductionRunner_CloseWaits(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `printf "hello"; exit 3`)
	rc, err := ProductionRunner(cmd)
	require.NoError(t, err)

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(out))

	err = rc.Close()
	require.Error(t, err, "Close should surface the non-zero exit code")
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr, "non-zero exit should surface as *exec.ExitError")
	assert.Equal(t, 3, exitErr.ExitCode())
}

func TestProductionRunner_CtxCancelKills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 30`)
	rc, err := ProductionRunner(cmd)
	require.NoError(t, err)

	cancel()

	done := make(chan error, 1)
	go func() { done <- rc.Close() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return within 5s after ctx cancel — runner hung")
	}
}

func TestBatchRunner_WrapsBytes(t *testing.T) {
	runner := batchRunner([]byte("x"))
	rc, err := runner(exec.Command("echo", "ignored"))
	require.NoError(t, err)

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "x", string(out))
	assert.NoError(t, rc.Close())
}

func TestBatchRunner_EmptyBytes(t *testing.T) {
	runner := batchRunner(nil)
	rc, err := runner(exec.Command("echo", "ignored"))
	require.NoError(t, err)

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Empty(t, out)
}
