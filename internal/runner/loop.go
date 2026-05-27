package runner

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// OuterLoopRunner drives multi-iteration dispatch for runtimes that
// can't run their own goal-evaluator loop (codex / gemini under
// DJ-136). claude-code is single-dispatch because the overlay's
// `/goal` directive owns iteration in the runtime itself.
//
// The runner is structured around a `DispatchOne` callback so the
// loop logic can be unit-tested without spinning up an ACP
// subprocess. Production wires `DispatchOne` to the same single-
// session dispatch claude-code uses; tests substitute a stub.
type OuterLoopRunner struct {
	// MaxIterations bounds the loop. The DJ specifies 20 as the
	// shipping default. Per-call override is supported for tests
	// that want a smaller ceiling.
	MaxIterations int

	// DispatchOne runs one iteration. Returns the agent's final
	// text + session id + session dir for the per-iteration
	// archive. iter is zero-based.
	DispatchOne func(ctx context.Context, iter int) (finalText, sessionID, sessionDir string, err error)

	// CheckConvergence inspects finalText for the verdict line and
	// returns true when the playbook reports `converged: true`. The
	// default impl is IsConverged (case-insensitive scan for the
	// canonical verdict line); tests can override.
	CheckConvergence func(finalText string) bool

	// Progress receives one human-readable line per iteration
	// transition so the operator sees the loop's cadence. Nil-safe.
	Progress io.Writer
}

// OuterLoopResult collects the per-iteration session ids and dirs
// plus the terminating finalText. The caller surfaces these in
// ActivityRun's fields (SessionID / SessionDir / FinalText reflect
// the LAST iteration for backward compat; SessionIDs / SessionDirs
// carry the full set).
type OuterLoopResult struct {
	Iterations  int
	Converged   bool // true when CheckConvergence returned true in some iteration
	SessionIDs  []string
	SessionDirs []string
	FinalText   string // last iteration's final text
}

// Run loops until convergence or the iteration ceiling. Returns
// the per-iteration session metadata. A nil DispatchOne or a
// non-positive MaxIterations is a programming error and returns
// immediately with an explicit error — the loop runner is internal,
// callers control its setup.
func (r *OuterLoopRunner) Run(ctx context.Context) (*OuterLoopResult, error) {
	if r.DispatchOne == nil {
		return nil, fmt.Errorf("outer loop: DispatchOne is required")
	}
	if r.MaxIterations <= 0 {
		return nil, fmt.Errorf("outer loop: MaxIterations must be > 0 (got %d)", r.MaxIterations)
	}
	check := r.CheckConvergence
	if check == nil {
		check = IsConverged
	}
	out := &OuterLoopResult{}
	for i := 0; i < r.MaxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		r.logf("→ iteration %d of %d", i+1, r.MaxIterations)
		finalText, sid, sdir, err := r.DispatchOne(ctx, i)
		out.Iterations++
		out.SessionIDs = append(out.SessionIDs, sid)
		out.SessionDirs = append(out.SessionDirs, sdir)
		out.FinalText = finalText
		if err != nil {
			return out, fmt.Errorf("outer loop: iteration %d: %w", i+1, err)
		}
		if check(finalText) {
			out.Converged = true
			r.logf("✓ converged after iteration %d", i+1)
			return out, nil
		}
		r.logf("⋯ iteration %d not converged; dispatching iteration %d", i+1, i+2)
	}
	r.logf("✗ iteration ceiling (%d) reached without convergence", r.MaxIterations)
	return out, nil
}

func (r *OuterLoopRunner) logf(format string, args ...any) {
	if r.Progress == nil {
		return
	}
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(r.Progress, "  [%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// convergedVerdictPattern matches the canonical verdict line the
// one-iteration playbook (DJ-136 phase 3) instructs the orchestrator
// to surface. Tolerant of leading whitespace + case so manual
// re-formatting doesn't accidentally disable convergence detection.
var convergedVerdictPattern = regexp.MustCompile(`(?im)^\s*converged:\s*true\b`)

// IsConverged scans the agent's final text for the canonical
// verdict line. The playbook is required to end with this line; a
// match anywhere in the text is sufficient (some agents wrap the
// verdict in additional reporting prose).
func IsConverged(finalText string) bool {
	// Trim trailing whitespace so a verdict line that ends the text
	// without a final newline is still matched by the anchored regex.
	trimmed := strings.TrimRight(finalText, " \t\n\r")
	return convergedVerdictPattern.MatchString(trimmed)
}
