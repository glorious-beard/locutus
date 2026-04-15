package scaffold_test

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

func TestScaffoldCreatesDirectories(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	dirs := []string{
		".borg",
		".borg/spec/features",
		".borg/spec/bugs",
		".borg/spec/decisions",
		".borg/spec/strategies",
		".borg/spec/entities",
		".borg/history",
		".borg/council/agents",
		".agents/skills",
	}
	for _, dir := range dirs {
		info, statErr := fsys.Stat(dir)
		assert.NoError(t, statErr, "directory should exist: %s", dir)
		if info != nil {
			assert.True(t, info.IsDir(), "should be a directory: %s", dir)
		}
	}
}

func TestScaffoldCreatesManifest(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "my-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/manifest.json")
	assert.NoError(t, err)

	var m spec.Manifest
	err = json.Unmarshal(data, &m)
	assert.NoError(t, err)

	assert.Equal(t, "my-project", m.ProjectName)
	assert.Equal(t, "0.1.0", m.Version)
	assert.False(t, m.CreatedAt.IsZero(), "created_at should be set")
}

func TestScaffoldCreatesTraces(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/spec/traces.json")
	assert.NoError(t, err)

	var idx spec.TraceabilityIndex
	err = json.Unmarshal(data, &idx)
	assert.NoError(t, err)

	assert.NotNil(t, idx.Entries, "entries should be initialized, not nil")
	assert.Empty(t, idx.Entries, "entries should be empty")
}

func TestScaffoldCreatesGoals(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "my-app")
	assert.NoError(t, err)

	data, err := fsys.ReadFile("GOALS.md")
	assert.NoError(t, err)

	assert.Contains(t, string(data), "my-app")
}

func TestScaffoldCreatesCouncilAgents(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	agents := []string{
		".borg/council/agents/planner.md",
		".borg/council/agents/critic.md",
		".borg/council/agents/researcher.md",
		".borg/council/agents/stakeholder.md",
		".borg/council/agents/historian.md",
		".borg/council/agents/convergence.md",
	}
	for _, agent := range agents {
		_, statErr := fsys.Stat(agent)
		assert.NoError(t, statErr, "agent file should exist: %s", agent)
	}
}

func TestScaffoldCreatesWorkflow(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/council/workflow.yaml")
	assert.NoError(t, err)
	assert.NotEmpty(t, data, "workflow.yaml should be non-empty")
}

func TestScaffoldIdempotent(t *testing.T) {
	fsys := specio.NewMemFS()

	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	err = scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err, "second run of Scaffold should not error")
}
