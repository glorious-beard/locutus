package dispatch

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// CommandRunner executes a prepared *exec.Cmd and returns a ReadCloser
// yielding the command's stdout. Closing the ReadCloser blocks until the
// process has exited and surfaces a non-nil *exec.ExitError if the process
// exited non-zero.
type CommandRunner func(cmd *exec.Cmd) (io.ReadCloser, error)

// ProductionRunner starts the command with a piped stdout and returns a
// ReadCloser that drains the pipe, waits for the process on Close, and
// surfaces any exit error from Wait.
func ProductionRunner(cmd *exec.Cmd) (io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return &processReadCloser{cmd: cmd, pipe: stdout}, nil
}

type processReadCloser struct {
	cmd  *exec.Cmd
	pipe io.ReadCloser
	once sync.Once
	err  error
}

func (p *processReadCloser) Read(buf []byte) (int, error) {
	return p.pipe.Read(buf)
}

// Close closes the stdout pipe and waits for the process to exit. The first
// call surfaces the exit status; subsequent calls return that same result.
func (p *processReadCloser) Close() error {
	p.once.Do(func() {
		// Close the pipe first to unblock any pending Read; then Wait for
		// the process. If the process was killed via ctx cancel on an
		// exec.CommandContext cmd, Wait returns with the signal-induced
		// error, which we surface.
		_ = p.pipe.Close()
		p.err = p.cmd.Wait()
	})
	return p.err
}

// batchRunner returns a CommandRunner that ignores the *exec.Cmd and yields
// the provided bytes as a ReadCloser. Used by tests to script output
// without spawning real processes.
func batchRunner(b []byte) CommandRunner {
	return func(_ *exec.Cmd) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(b)), nil
	}
}
