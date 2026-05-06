package render

import (
	"fmt"
	"strings"
)

// RenderDiff returns a compact unified diff between old and new
// strings. Operates on lines; sufficient for the rendered Markdown
// the refine path produces. headerOld and headerNew label the diff
// origins (e.g. "before refine" / "after refine: brief='...'") so
// the user can read what's being compared at a glance.
//
// The output uses standard unified-diff conventions (---, +++, @@
// hunk headers, +/- prefixes). Hunks are computed via Myers' LCS
// on the lines. When inputs are byte-identical, returns an empty
// string so callers can branch on "any change" cheaply.
func RenderDiff(headerOld, headerNew, old, new string) string {
	if old == new {
		return ""
	}
	a := splitLines(old)
	b := splitLines(new)
	hunks := unifiedHunks(a, b, 3)
	if len(hunks) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", headerOld)
	fmt.Fprintf(&out, "+++ %s\n", headerNew)
	for _, h := range hunks {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", h.aStart+1, h.aLen, h.bStart+1, h.bLen)
		for _, l := range h.lines {
			out.WriteString(l)
			if !strings.HasSuffix(l, "\n") {
				out.WriteString("\n")
			}
		}
	}
	return out.String()
}

// splitLines splits s into lines preserving the trailing newline on
// each, so reconstituting them produces the original text. Empty
// inputs yield an empty slice (not [""]) so the LCS loop sees the
// natural "no lines" state.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

type diffHunk struct {
	aStart, aLen int
	bStart, bLen int
	lines        []string // already prefixed with " ", "+", or "-"
}

// unifiedHunks computes diff hunks with `context` lines of context
// surrounding each change region. Implementation: classic LCS via
// dynamic programming, then walk the LCS to emit a per-line edit
// script, then group consecutive non-context edits into hunks.
//
// O(n*m) memory; fine for refine-sized inputs (a single rendered
// node Markdown is hundreds of lines max). Drop in a smarter
// algorithm if a node ever balloons past that.
func unifiedHunks(a, b []string, context int) []diffHunk {
	ops := lcsEditScript(a, b)
	if len(ops) == 0 {
		return nil
	}

	// Find regions with edits, expand by `context` lines, merge
	// overlapping regions.
	type region struct{ start, end int } // indices into ops
	var regions []region
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := i-context, i+context+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(ops) {
			hi = len(ops)
		}
		if len(regions) > 0 && regions[len(regions)-1].end >= lo {
			regions[len(regions)-1].end = hi
		} else {
			regions = append(regions, region{lo, hi})
		}
	}

	// Convert each region into a hunk with translated line numbers.
	var hunks []diffHunk
	for _, r := range regions {
		var h diffHunk
		// Compute aStart / bStart by walking ops up to r.start.
		ai, bi := 0, 0
		for i := 0; i < r.start; i++ {
			switch ops[i].kind {
			case ' ':
				ai++
				bi++
			case '-':
				ai++
			case '+':
				bi++
			}
		}
		h.aStart = ai
		h.bStart = bi
		for i := r.start; i < r.end; i++ {
			op := ops[i]
			switch op.kind {
			case ' ':
				h.aLen++
				h.bLen++
				h.lines = append(h.lines, " "+op.line)
			case '-':
				h.aLen++
				h.lines = append(h.lines, "-"+op.line)
			case '+':
				h.bLen++
				h.lines = append(h.lines, "+"+op.line)
			}
		}
		hunks = append(hunks, h)
	}
	return hunks
}

type editOp struct {
	kind byte // ' ', '+', '-'
	line string
}

// lcsEditScript returns the edit script that transforms a into b via
// inserts/deletes/keeps, walking the LCS table backwards.
func lcsEditScript(a, b []string) []editOp {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	// dp[i][j] = LCS length of a[:i] vs b[:j].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var ops []editOp
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append(ops, editOp{' ', a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, editOp{'+', b[j-1]})
			j--
		default:
			ops = append(ops, editOp{'-', a[i-1]})
			i--
		}
	}
	// Reverse — we built it back-to-front.
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}
