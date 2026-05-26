// DJ-136 phase 5 — outer-loop unit tests. The loop wraps
// ACP dispatch on Codex / Gemini; testing it directly requires
// no subprocesses since DispatchOne is a callback.

package runner

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunnerOuterLoop_DispatchesUntilConverged — given a stub
// dispatcher whose convergence-check returns false twice then
// true, the outer loop dispatches three iterations and stops.
func TestRunnerOuterLoop_DispatchesUntilConverged(t *testing.T) {
	var calls int
	r := &OuterLoopRunner{
		MaxIterations: 20,
		DispatchOne: func(ctx context.Context, iter int) (string, string, string, error) {
			calls++
			text := "converged: false; still axes open"
			if calls == 3 {
				text = "summary: 3 axes decided.\nconverged: true"
			}
			return text, fmt.Sprintf("sid-%d", iter), fmt.Sprintf("dir-%d", iter), nil
		},
	}
	res, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, res.Iterations)
	assert.True(t, res.Converged)
	assert.Equal(t, []string{"sid-0", "sid-1", "sid-2"}, res.SessionIDs)
}

// TestRunnerOuterLoop_RespectsIterationCeiling — when the
// convergence check never returns true, the loop stops at the
// configured ceiling and Converged stays false.
func TestRunnerOuterLoop_RespectsIterationCeiling(t *testing.T) {
	r := &OuterLoopRunner{
		MaxIterations: 5,
		DispatchOne: func(ctx context.Context, iter int) (string, string, string, error) {
			return "converged: false; never done", "sid", "dir", nil
		},
	}
	res, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, res.Iterations)
	assert.False(t, res.Converged)
}

// TestRunnerOuterLoop_PropagatesDispatchError — when DispatchOne
// fails, the loop returns the error with partial session
// metadata so the operator can investigate.
func TestRunnerOuterLoop_PropagatesDispatchError(t *testing.T) {
	r := &OuterLoopRunner{
		MaxIterations: 3,
		DispatchOne: func(ctx context.Context, iter int) (string, string, string, error) {
			if iter == 1 {
				return "", "sid-1", "dir-1", fmt.Errorf("acp prompt failed")
			}
			return "converged: false; iterating", "sid-0", "dir-0", nil
		},
	}
	res, err := r.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration 2")
	assert.Equal(t, 2, res.Iterations)
	assert.False(t, res.Converged)
}

// TestRunnerOuterLoop_ProgressWriterReceivesPerIterationLine —
// the operator sees one line per iteration transition through the
// progress writer.
func TestRunnerOuterLoop_ProgressWriterReceivesPerIterationLine(t *testing.T) {
	var buf bytes.Buffer
	r := &OuterLoopRunner{
		MaxIterations: 2,
		Progress:      &buf,
		DispatchOne: func(ctx context.Context, iter int) (string, string, string, error) {
			if iter == 1 {
				return "converged: true", "sid", "dir", nil
			}
			return "converged: false; iterating", "sid", "dir", nil
		},
	}
	_, err := r.Run(context.Background())
	require.NoError(t, err)
	got := buf.String()
	assert.Contains(t, got, "iteration 1 of 2")
	assert.Contains(t, got, "iteration 2 of 2")
	assert.Contains(t, got, "converged after iteration 2")
}

// TestIsConverged_RecognizesVerdictLine — the canonical "converged:
// true" line is matched in several plausible positions (last line,
// last line with trailing newline, embedded in a longer report).
func TestIsConverged_RecognizesVerdictLine(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		match bool
	}{
		{"bare verdict", "converged: true", true},
		{"trailing newline", "converged: true\n", true},
		{"with summary above", "summary: 3 axes decided\nconverged: true\n", true},
		{"capitalized", "Converged: True", true},
		{"false verdict", "converged: false; 3 axes open", false},
		{"empty", "", false},
		{"partial match — substring not on own line", "converged: trueish", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.match, IsConverged(c.text), "input was %q", c.text)
		})
	}
}

// TestRunnerOuterLoop_ZeroMaxIsAnError — defensive: prevents
// silent zero-iteration runs caused by misconfiguration.
func TestRunnerOuterLoop_ZeroMaxIsAnError(t *testing.T) {
	r := &OuterLoopRunner{
		DispatchOne: func(ctx context.Context, iter int) (string, string, string, error) {
			return "", "", "", nil
		},
	}
	_, err := r.Run(context.Background())
	require.Error(t, err)
}
