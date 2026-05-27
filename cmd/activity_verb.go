package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/runner"
	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/glorious-beard/locutus/internal/specio"
)

// runActivityVerb is the shared entry point used by every Locutus
// verb that dispatches an activity playbook (refine / import / adopt
// / assimilate). It loads the canonical playbook, appends any
// per-invocation context, resolves the runtime, and streams the
// ACP session to stdout.
//
// Per DJ-135 phase 5 Q3 (a), the verb blocks until the ACP session
// closes. The session's final summary lands on stdout; the session
// directory under .locutus/sessions/ carries the full event log.
func runActivityVerb(ctx context.Context, _ *CLI, activityName, contextNote string) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}
	reg, err := activity.NewRegistry(fsys)
	if err != nil {
		return fmt.Errorf("%s: activity registry: %w", activityName, err)
	}
	// Resolve runtime up front so the overlay-aware playbook loader
	// can prefer .borg/plans/<activity>.<runtime>.md over the default
	// <activity>.md. The runtime is then passed through to the runner
	// — single resolution per dispatch.
	runtime, err := reg.Resolve(activityName, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", activityName, err)
	}
	act, ok := reg.Lookup(activityName)
	if !ok {
		// Defensive: Resolve succeeded so Lookup must too — guarded
		// against future refactors that might separate the two.
		return fmt.Errorf("%s: activity resolved but missing from registry", activityName)
	}
	playbook, source, err := loadActivityPlaybook(fsys, activityName, runtime)
	if err != nil {
		return fmt.Errorf("%s: %w", activityName, err)
	}
	prompt := playbook
	if contextNote != "" {
		prompt = playbook + "\n\n---\n\n## Run context\n\n" + contextNote + "\n"
	}
	// Quick start banner so the operator sees we're going. Stderr
	// for operational messaging; stdout is reserved for the agent's
	// own text output so pipes work cleanly. The source path tells
	// the operator which playbook variant (default vs overlay) is
	// driving this run.
	fmt.Fprintf(os.Stderr, "→ dispatching %s activity (runtime=%s, playbook=%s)\n", activityName, runtime, source)
	run, err := runner.DispatchActivity(ctx, root, activityName, runtime, prompt, act.MaxIterations, os.Stdout, os.Stderr)
	if err != nil {
		return fmt.Errorf("%s: dispatch: %w", activityName, err)
	}
	fmt.Fprintf(os.Stderr, "\n→ session: %s (runtime=%s)\n", run.SessionDir, run.Runtime)
	return nil
}

// loadActivityPlaybook reads the playbook for activityName from
// .borg/plans/, preferring the runtime-specific overlay
// <activity>.<runtime>.md when present and falling back to
// <activity>.md. Missing default surfaces as a clean error mentioning
// the canonical path so operators can author or restore it.
//
// Returns (body, sourcePath, err). sourcePath is surfaced in the
// dispatch banner so the operator sees which file produced the body.
func loadActivityPlaybook(fsys specio.FS, activityName, runtime string) (string, string, error) {
	// ACP dispatch always uses the headless body (DJ-140); the
	// interactive variant is selected by the publisher for slash commands.
	data, source, err := scaffold.ResolvePlaybook(fsys, ".borg/plans", activityName, runtime, scaffold.ModeHeadless)
	if err != nil {
		return "", source, err
	}
	return string(data), source, nil
}
