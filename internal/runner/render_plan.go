package runner

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
)

// renderPlanBlock writes one human-readable multi-line plan block
// for an EventPlan to w. The header carries the entry count; each
// entry is a single indented line with a status glyph.
//
// Status glyphs:
//
//	○ pending
//	⟳ in_progress
//	✓ completed
//
// Priority is captured on the dispatch event but not surfaced in
// the default render — it stays on AgentEvent.Raw for archive
// consumers (and future renderers that want a high-priority
// highlight).
//
// An empty plan renders as a single "cleared" line rather than a
// header followed by zero entries; agents that retract their plan
// should produce a recognizably distinct line, not an empty block.
//
// now is injected for testability — pass time.Now in production and
// a fixed clock in tests so the timestamp prefix is deterministic.
func renderPlanBlock(w io.Writer, entries []dispatch.PlanEntry, now func() time.Time) {
	ts := now().Format("15:04:05")
	if len(entries) == 0 {
		fmt.Fprintf(w, "  [%s] plan (0 entries) — cleared\n", ts)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  [%s] plan (%d entries):\n", ts, len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "       %s %s\n", statusGlyph(e.Status), e.Content)
	}
	io.WriteString(w, b.String())
}

func statusGlyph(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "in_progress":
		return "⟳"
	case "pending":
		return "○"
	default:
		// Unknown status — render as the literal so the operator can
		// see the protocol gap rather than swallowing it under a
		// best-guess glyph.
		return "?"
	}
}
