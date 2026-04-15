package check

import (
	"fmt"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// mockCommander implements Commander using a map of command strings to errors.
// Commands not in the map succeed with output "ok".
type mockCommander struct {
	failures map[string]error
}

func (m *mockCommander) Run(name string, args ...string) ([]byte, error) {
	// Reconstruct the full command string for lookup.
	cmd := name
	for _, a := range args {
		cmd += " " + a
	}
	if err, ok := m.failures[cmd]; ok {
		return []byte(""), err
	}
	return []byte("ok"), nil
}

func TestCheckAllPass(t *testing.T) {
	cmd := &mockCommander{failures: map[string]error{}}

	strategies := []spec.Strategy{
		{
			ID:            "s-001",
			Title:         "Go toolchain",
			Prerequisites: []string{"go version", "gofmt -l ."},
		},
		{
			ID:            "s-002",
			Title:         "Node toolchain",
			Prerequisites: []string{"node --version", "npm --version"},
		},
	}

	results := Check(cmd, strategies)

	assert.Len(t, results, 2)

	for _, r := range results {
		assert.Empty(t, r.Failed, "strategy %s should have no failures", r.StrategyID)
		assert.Len(t, r.Passed, 2, "strategy %s should have 2 passed prerequisites", r.StrategyID)
	}

	// Verify strategy metadata is propagated.
	assert.Equal(t, "s-001", results[0].StrategyID)
	assert.Equal(t, "Go toolchain", results[0].StrategyTitle)
	assert.Equal(t, "s-002", results[1].StrategyID)
	assert.Equal(t, "Node toolchain", results[1].StrategyTitle)
}

func TestCheckSomeFail(t *testing.T) {
	cmd := &mockCommander{
		failures: map[string]error{
			"python3 --version": fmt.Errorf("python3: command not found"),
		},
	}

	strategies := []spec.Strategy{
		{
			ID:    "s-010",
			Title: "Python setup",
			Prerequisites: []string{
				"go version",
				"python3 --version",
				"node --version",
			},
		},
	}

	results := Check(cmd, strategies)

	assert.Len(t, results, 1)
	r := results[0]
	assert.Equal(t, "s-010", r.StrategyID)
	assert.Equal(t, "Python setup", r.StrategyTitle)

	assert.Len(t, r.Passed, 2)
	assert.Contains(t, r.Passed, "go version")
	assert.Contains(t, r.Passed, "node --version")

	assert.Len(t, r.Failed, 1)
	assert.Equal(t, "python3 --version", r.Failed[0].Prerequisite)
	assert.Equal(t, "python3: command not found", r.Failed[0].Err)
}

func TestCheckNoPrerequisites(t *testing.T) {
	cmd := &mockCommander{failures: map[string]error{}}

	strategies := []spec.Strategy{
		{
			ID:            "s-020",
			Title:         "No prereqs",
			Prerequisites: []string{},
		},
	}

	results := Check(cmd, strategies)

	assert.Len(t, results, 1)
	r := results[0]
	assert.Equal(t, "s-020", r.StrategyID)
	assert.Empty(t, r.Passed)
	assert.Empty(t, r.Failed)
}

func TestCheckMultipleStrategies(t *testing.T) {
	cmd := &mockCommander{
		failures: map[string]error{
			"docker --version":  fmt.Errorf("docker: command not found"),
			"kubectl version":   fmt.Errorf("kubectl: command not found"),
		},
	}

	strategies := []spec.Strategy{
		{
			ID:            "s-100",
			Title:         "Go only",
			Prerequisites: []string{"go version"},
		},
		{
			ID:            "s-101",
			Title:         "Containers",
			Prerequisites: []string{"docker --version", "kubectl version"},
		},
		{
			ID:            "s-102",
			Title:         "Mixed",
			Prerequisites: []string{"go version", "docker --version", "node --version"},
		},
	}

	results := Check(cmd, strategies)

	assert.Len(t, results, 3)

	// s-100: all pass
	assert.Equal(t, "s-100", results[0].StrategyID)
	assert.Equal(t, "Go only", results[0].StrategyTitle)
	assert.Len(t, results[0].Passed, 1)
	assert.Empty(t, results[0].Failed)

	// s-101: all fail
	assert.Equal(t, "s-101", results[1].StrategyID)
	assert.Equal(t, "Containers", results[1].StrategyTitle)
	assert.Empty(t, results[1].Passed)
	assert.Len(t, results[1].Failed, 2)

	// s-102: mixed
	assert.Equal(t, "s-102", results[2].StrategyID)
	assert.Equal(t, "Mixed", results[2].StrategyTitle)
	assert.Len(t, results[2].Passed, 2)
	assert.Contains(t, results[2].Passed, "go version")
	assert.Contains(t, results[2].Passed, "node --version")
	assert.Len(t, results[2].Failed, 1)
	assert.Equal(t, "docker --version", results[2].Failed[0].Prerequisite)
}
