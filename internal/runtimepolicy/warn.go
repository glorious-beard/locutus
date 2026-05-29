package runtimepolicy

import "fmt"

// CheckVersion compares a runtime's detected version against its
// declared floor. It returns (message, warn): warn is true ONLY when
// a floor is declared, the detected version is parseable, and it is
// strictly below the floor. In every other case — no floor declared,
// unparseable/empty detected version, unknown runtime — warn is false
// (fail-open, DJ-144 §9). The message is empty when warn is false.
//
// CheckVersion never blocks; the caller logs the message and proceeds.
func (r *Registry) CheckVersion(runtime, detected string) (message string, warn bool) {
	floor, ok := r.MinVersion(runtime)
	if !ok {
		return "", false
	}
	below, ok := BelowFloor(detected, floor)
	if !ok || !below {
		return "", false
	}
	return fmt.Sprintf(
		"runtime %q version %q is below the minimum %q Locutus relies on; "+
			"version-gated features (e.g. Claude Code dynamic workflows) may silently "+
			"degrade — see docs/runtime-affordances.md",
		runtime, detected, floor,
	), true
}
