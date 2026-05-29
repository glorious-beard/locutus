package runtimepolicy

import (
	"strconv"
	"strings"
)

// BelowFloor reports whether the detected version is strictly below
// the floor. The second return is false when the comparison is
// indeterminate (either string is empty or unparseable) — callers
// MUST fail open on ok==false: never warn or block on a version we
// could not read (DJ-144 §9).
//
// Parsing is deliberately lenient: a leading "v" is stripped, a
// trailing "-label"/"+build" suffix is dropped, and the dotted
// numeric prefix is compared field-by-field. Non-numeric or missing
// fields make the version unparseable (ok=false). This is not a full
// semver implementation — it is the minimum needed to compare
// coding-agent CLI version strings, and it errs toward fail-open.
func BelowFloor(detected, floor string) (below bool, ok bool) {
	d, dok := parseVersion(detected)
	f, fok := parseVersion(floor)
	if !dok || !fok {
		return false, false
	}
	return compare(d, f) < 0, true
}

func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return out, false
	}
	// Drop pre-release / build metadata: "2.1.154-beta+x" → "2.1.154".
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func compare(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}
