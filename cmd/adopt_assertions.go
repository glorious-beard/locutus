package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/eval"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// runAssertions evaluates every Assertion on the given Approach and
// returns the outcomes. Used by verify() to decide whether an Approach
// transitioned to `live` or `failed`.
//
// Mechanical kinds (test_pass, command_exit_zero, file_exists, etc.) run
// directly against the repo on disk. llm_review routes through
// eval.Runner, which invokes LLMJudge with the Approach body + assertion
// prompt + artifact contents (read from fsys, not repoDir — artifacts may
// live in a MemFS under test). A nil runner surfaces as an explicit
// failure on llm_review; the non-LLM kinds are unaffected.
func runAssertions(ctx context.Context, approach spec.Approach, repoDir string, runner *eval.Runner, fsys specio.FS) []state.AssertionResult {
	results := make([]state.AssertionResult, 0, len(approach.Assertions))
	for _, a := range approach.Assertions {
		passed, output := evaluateAssertion(ctx, a, approach, repoDir, runner, fsys)
		results = append(results, state.AssertionResult{
			Assertion: a,
			Passed:    passed,
			Output:    output,
			RunAt:     time.Now(),
		})
	}
	return results
}

// allPassed returns true only if every assertion passed. An empty slice is
// treated as success — an Approach with no assertions is live as long as
// the agent run succeeded.
func allPassed(results []state.AssertionResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

func evaluateAssertion(ctx context.Context, a spec.Assertion, approach spec.Approach, repoDir string, runner *eval.Runner, fsys specio.FS) (bool, string) {
	switch a.Kind {
	case spec.AssertionKindCommandExitZero:
		return runShell(ctx, a.Target, repoDir)
	case spec.AssertionKindTestPass:
		target := a.Target
		if target == "" {
			target = "./..."
		}
		return runShell(ctx, "go test "+target, repoDir)
	case spec.AssertionKindCompiles:
		return runShell(ctx, "go build ./...", repoDir)
	case spec.AssertionKindLintClean:
		return runShell(ctx, "go vet ./...", repoDir)
	case spec.AssertionKindFileExists:
		return fileExists(filepath.Join(repoDir, a.Target))
	case spec.AssertionKindFileNotExists:
		ok, msg := fileExists(filepath.Join(repoDir, a.Target))
		if ok {
			return false, "file unexpectedly present: " + a.Target
		}
		if strings.HasPrefix(msg, "stat ") {
			// The stat failed for a reason other than "not exists" — surface it.
			return false, msg
		}
		return true, "confirmed absent: " + a.Target
	case spec.AssertionKindContains:
		return fileContains(filepath.Join(repoDir, a.Target), a.Pattern, true)
	case spec.AssertionKindNotContains:
		return fileContains(filepath.Join(repoDir, a.Target), a.Pattern, false)
	case spec.AssertionKindLLMReview:
		return evaluateLLMReview(ctx, a, approach, runner, fsys)
	default:
		return false, "unknown assertion kind: " + string(a.Kind)
	}
}

// evaluateLLMReview reads artifact contents and hands the case to the
// eval.Runner. A nil runner or nil LLM surfaces as a failed verdict with
// a clear message — we never silently pass an llm_review just because the
// provider isn't wired up. That matches DJ-035's "independent reviewer
// LLM call" intent: absence of a reviewer is not success.
func evaluateLLMReview(ctx context.Context, a spec.Assertion, approach spec.Approach, runner *eval.Runner, fsys specio.FS) (bool, string) {
	if runner == nil {
		return false, "llm_review requires an llm provider; none configured"
	}
	artifacts := readArtifactContents(fsys, approach.ArtifactPaths)
	metric, err := runner.Evaluate(ctx, a.Kind, eval.EvalCase{
		Approach:  approach,
		Assertion: a,
		Artifacts: artifacts,
	})
	if err != nil {
		return false, fmt.Sprintf("llm_review: %v", err)
	}
	out := metric.Reasoning
	if out == "" {
		if metric.Passed {
			out = "llm_review passed (no reasoning provided)"
		} else {
			out = "llm_review failed (no reasoning provided)"
		}
	}
	return metric.Passed, out
}

// readArtifactContents loads the full body of every ArtifactPath from
// fsys. Files that fail to read are recorded as placeholder strings so
// the judge sees "this file was supposed to be here but wasn't" rather
// than a silent omission. Per-file size capping lives in LLMJudge, not
// here — we give the judge everything and let it apply its own limits.
func readArtifactContents(fsys specio.FS, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := fsys.ReadFile(p)
		if err != nil {
			out[p] = fmt.Sprintf("(unreadable: %v)", err)
			continue
		}
		out[p] = string(data)
	}
	return out
}

// runShell executes a command string (shell-style with space-separated
// tokens) in repoDir with a 60s timeout. Returns (passed, combined-output).
// ctx cancellation kills the subprocess via exec.CommandContext; the 60s
// cap is layered on top via context.WithTimeout so a parent ctx cancel
// (e.g. user Ctrl-C) takes precedence over the assertion-level timeout.
// No PATH munging; relies on the caller's environment.
func runShell(ctx context.Context, cmdline, repoDir string) (bool, string) {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false, "empty command"
	}
	fields := strings.Fields(cmdline)

	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	c := exec.CommandContext(cmdCtx, fields[0], fields[1:]...)
	c.Dir = repoDir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return false, fmt.Sprintf("%s: timed out after 60s\n%s", cmdline, buf.String())
		}
		if ctx.Err() != nil {
			return false, fmt.Sprintf("%s: %v\n%s", cmdline, ctx.Err(), buf.String())
		}
		return false, fmt.Sprintf("%s: %v\n%s", cmdline, err, buf.String())
	}
	return true, buf.String()
}

func fileExists(path string) (bool, string) {
	info, err := statFile(path)
	if err != nil {
		return false, "stat " + path + ": " + err.Error()
	}
	if info.IsDir() {
		return true, "directory exists: " + path
	}
	return true, "file exists: " + path
}

func fileContains(path, pattern string, wantMatch bool) (bool, string) {
	data, err := readFile(path)
	if err != nil {
		if wantMatch {
			return false, "read " + path + ": " + err.Error()
		}
		// not_contains: file missing trivially satisfies "doesn't contain."
		return true, "file absent: " + path
	}
	found := strings.Contains(string(data), pattern)
	switch {
	case wantMatch && found:
		return true, "match found in " + path
	case wantMatch && !found:
		return false, "pattern not found in " + path
	case !wantMatch && found:
		return false, "unexpected match in " + path
	default:
		return true, "pattern correctly absent from " + path
	}
}
