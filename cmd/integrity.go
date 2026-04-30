package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/agent"
)

// formatIntegrityViolation renders an agent.IntegrityViolationError
// as a multi-line stderr message naming each dangling reference. The
// list is what the user actually needs — it tells them which decisions
// the architect forgot to emit, which they can address by re-running
// (Pro Preview is stochastic), switching to a stronger model, or
// hand-editing the produced spec.
//
// Returns false when err is not an IntegrityViolationError; callers
// fall through to default error handling in that case.
func formatIntegrityViolation(err error) (string, bool) {
	var iv *agent.IntegrityViolationError
	if !errors.As(err, &iv) {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "spec-generation council failed: %d dangling reference(s) after %d revise attempt(s).\n",
		len(iv.Warnings), iv.Attempts)
	b.WriteString("The architect emitted nodes that reference IDs it never produced. ")
	b.WriteString("Either re-run, configure a stronger architect model via LOCUTUS_MODELS_CONFIG, ")
	b.WriteString("or hand-edit the proposal.\n\nDangling references:\n")
	for _, w := range iv.Warnings {
		fmt.Fprintf(&b, "  - %s\n", w.String())
	}
	return strings.TrimRight(b.String(), "\n"), true
}
