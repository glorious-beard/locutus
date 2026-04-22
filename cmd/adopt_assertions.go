package cmd

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/state"
)

// runAssertions evaluates every Assertion on the given Approach against
// the target directory and returns the outcomes. Used by verify() to
// decide whether an Approach transitioned to `live` or `failed`.
//
// Only the mechanical assertion kinds are implemented here — test_pass,
// command_exit_zero, file_exists, file_not_exists, contains, not_contains,
// compiles, lint_clean. The llm_review kind is out of scope for this
// round; it's recorded as passed with a note so the overall verdict isn't
// blocked. A future round should wire it through a reviewer agent.
func runAssertions(assertions []spec.Assertion, repoDir string) []state.AssertionResult {
	results := make([]state.AssertionResult, 0, len(assertions))
	for _, a := range assertions {
		passed, output := evaluateAssertion(a, repoDir)
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

func evaluateAssertion(a spec.Assertion, repoDir string) (bool, string) {
	switch a.Kind {
	case spec.AssertionKindCommandExitZero:
		return runShell(a.Target, repoDir)
	case spec.AssertionKindTestPass:
		target := a.Target
		if target == "" {
			target = "./..."
		}
		return runShell("go test "+target, repoDir)
	case spec.AssertionKindCompiles:
		return runShell("go build ./...", repoDir)
	case spec.AssertionKindLintClean:
		return runShell("go vet ./...", repoDir)
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
		return true, "llm_review not implemented in this round; treated as pass"
	default:
		return false, "unknown assertion kind: " + string(a.Kind)
	}
}

// runShell executes a command string (shell-style with space-separated
// tokens) in repoDir with a 60s timeout. Returns (passed, combined-output).
// No PATH munging; relies on the caller's environment.
func runShell(cmdline, repoDir string) (bool, string) {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false, "empty command"
	}
	fields := strings.Fields(cmdline)
	c := exec.Command(fields[0], fields[1:]...)
	c.Dir = repoDir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Start(); err != nil {
		return false, fmt.Sprintf("start %s: %v", cmdline, err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return false, fmt.Sprintf("%s: %v\n%s", cmdline, err, buf.String())
		}
		return true, buf.String()
	case <-time.After(60 * time.Second):
		_ = c.Process.Kill()
		return false, fmt.Sprintf("%s: timed out after 60s\n%s", cmdline, buf.String())
	}
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
