// DJ-136 phase 2 — renderer unit tests for the multi-line plan block.

package runner

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"

	"github.com/stretchr/testify/assert"
)

// fixedClock returns a now-func that always reports the same instant.
// Keeps test output deterministic without exporting time globals.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// TestEventPlan_RenderInProgressWriter — a 3-entry plan produces the
// expected multi-line block with correct glyphs and a fixed timestamp.
func TestEventPlan_RenderInProgressWriter(t *testing.T) {
	entries := []dispatch.PlanEntry{
		{Content: "Read GOALS.md", Status: "completed"},
		{Content: "Dispatch spec-scout for axis survey", Status: "in_progress"},
		{Content: "Commit decisions from scout's open axes", Status: "pending"},
	}
	var buf bytes.Buffer
	renderPlanBlock(&buf, entries, fixedClock(time.Date(2026, 5, 26, 12, 18, 55, 0, time.UTC)))
	got := buf.String()

	// Header line carries the count.
	assert.Contains(t, got, "[12:18:55] plan (3 entries):")
	// One line per entry with the right glyph.
	assert.Contains(t, got, "✓ Read GOALS.md")
	assert.Contains(t, got, "⟳ Dispatch spec-scout for axis survey")
	assert.Contains(t, got, "○ Commit decisions from scout's open axes")
	// Exactly four lines (header + 3 entries).
	assert.Equal(t, 4, strings.Count(got, "\n"), "header + N entries; %q", got)
}

// TestEventPlan_EmptyEntriesRendersGracefully — an empty plan
// renders a single "cleared" line, not a header + zero entries.
func TestEventPlan_EmptyEntriesRendersGracefully(t *testing.T) {
	var buf bytes.Buffer
	renderPlanBlock(&buf, nil, fixedClock(time.Date(2026, 5, 26, 12, 18, 55, 0, time.UTC)))
	got := buf.String()
	assert.Equal(t, "  [12:18:55] plan (0 entries) — cleared\n", got)
}

// TestEventPlan_BackfillsWithToolCallProgressMutex — concurrent
// emission of plan + tool-call lines does not interleave when both
// writers go through the same mutex around the multi-line render.
// The block must land as a contiguous group; no other writer's line
// may appear between the header and the last entry.
func TestEventPlan_BackfillsWithToolCallProgressMutex(t *testing.T) {
	entries := []dispatch.PlanEntry{
		{Content: "step one", Status: "in_progress"},
		{Content: "step two", Status: "pending"},
		{Content: "step three", Status: "pending"},
	}
	var (
		buf bytes.Buffer
		mu  sync.Mutex
		wg  sync.WaitGroup
	)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				mu.Lock()
				renderPlanBlock(&buf, entries, fixedClock(time.Unix(int64(i), 0).UTC()))
				mu.Unlock()
			} else {
				mu.Lock()
				buf.WriteString("  tool-call line\n")
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	// For each plan-header occurrence, confirm the following two
	// lines are entry lines (start with the glyph indent) — i.e.
	// the block is contiguous.
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, ln := range lines {
		if !strings.Contains(ln, "plan (3 entries):") {
			continue
		}
		// Following 3 lines must be entry lines (prefix "       ").
		for j := 1; j <= 3; j++ {
			next := lines[i+j]
			assert.True(t, strings.HasPrefix(next, "       "),
				"line %d after plan header was %q (interleaved with another writer)", i+j, next)
		}
	}
}
