package activity

import (
	"fmt"
	"os/exec"

	"github.com/glorious-beard/locutus/internal/dispatch/acp"
)

// Resolver picks a runtime for an activity by walking its preference
// list and returning the first runtime whose ACP-server binary is
// detectable on $PATH. The detection delegates to exec.LookPath
// against the Cmd registered in acp.AgentSpawns — same mechanism
// `locutus init`'s preflight uses.
//
// A nil LookPath uses exec.LookPath; tests pass a stub to control
// which runtimes appear "installed."
type Resolver struct {
	// LookPath, if non-nil, replaces exec.LookPath. Set in tests to
	// simulate runtimes being present or absent without touching the
	// host environment.
	LookPath func(name string) (string, error)
}

// Resolve walks the activity's runtime preference list in order and
// returns the first runtime whose binary is reachable. Returns an
// error when:
//   - the activity name is unknown to the registry, OR
//   - none of the listed runtimes have their binaries on $PATH.
//
// The "none installed" error names every checked runtime + the
// install hint for each so the operator can pick one and install it.
// Cheaper to surface this at first-dispatch than to fail mid-call
// with a Spawn error from acp.
func (r *Registry) Resolve(activityName string, resolver *Resolver) (string, error) {
	act, ok := r.Lookup(activityName)
	if !ok {
		return "", fmt.Errorf("activity %q is not registered; available: %v", activityName, r.Names())
	}
	lookup := exec.LookPath
	if resolver != nil && resolver.LookPath != nil {
		lookup = resolver.LookPath
	}

	var checked []string
	for _, runtimeID := range act.Runtimes {
		spawn, ok := acp.AgentSpawns[runtimeID]
		if !ok {
			// validateRegistry already rejects unknown runtimes at
			// load time; this branch is defensive against a registry
			// constructed directly (bypassing NewRegistry).
			return "", fmt.Errorf("activity %q: runtime %q not in acp.AgentSpawns", activityName, runtimeID)
		}
		if _, err := lookup(spawn.Cmd); err == nil {
			return runtimeID, nil
		}
		checked = append(checked, fmt.Sprintf("%s (binary %q)", runtimeID, spawn.Cmd))
	}
	return "", fmt.Errorf("activity %q: no runtime available — checked %v; install one via `locutus init`'s preflight hints", activityName, checked)
}
