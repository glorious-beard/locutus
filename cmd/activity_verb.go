package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/runner"
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
	playbook, err := loadActivityPlaybook(fsys, activityName)
	if err != nil {
		return fmt.Errorf("%s: %w", activityName, err)
	}
	prompt := playbook
	if contextNote != "" {
		prompt = playbook + "\n\n---\n\n## Run context\n\n" + contextNote + "\n"
	}
	run, err := runner.DispatchActivity(ctx, root, activityName, prompt, reg, os.Stdout)
	if err != nil {
		return fmt.Errorf("%s: dispatch: %w", activityName, err)
	}
	fmt.Fprintf(os.Stderr, "\n→ session: %s (runtime=%s)\n", run.SessionDir, run.Runtime)
	return nil
}

// loadActivityPlaybook reads .borg/plans/<activity>.md and returns
// its content. Missing playbook surfaces as a clean error mentioning
// the canonical path so operators can author or restore it.
func loadActivityPlaybook(fsys specio.FS, activityName string) (string, error) {
	path := ".borg/plans/" + activityName + ".md"
	data, err := fsys.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "file does not exist") {
			return "", fmt.Errorf("playbook %s missing — run `locutus update --offline --reset` to restore", path)
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}
