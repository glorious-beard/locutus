package scaffold_test

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		".borg/agents",
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

func TestScaffoldCreatesAgents(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	// All 15 agent definition files should exist under .borg/agents/.
	agents := []string{
		".borg/agents/planner.md",
		".borg/agents/critic.md",
		".borg/agents/researcher.md",
		".borg/agents/stakeholder.md",
		".borg/agents/historian.md",
		".borg/agents/convergence.md",
		".borg/agents/scout.md",
		".borg/agents/backend_analyzer.md",
		".borg/agents/frontend_analyzer.md",
		".borg/agents/infra_analyzer.md",
		".borg/agents/gap_analyst.md",
		".borg/agents/remediator.md",
		".borg/agents/validator.md",
		".borg/agents/guide.md",
		".borg/agents/reviewer.md",
	}
	for _, agent := range agents {
		_, statErr := fsys.Stat(agent)
		assert.NoError(t, statErr, "agent file should exist: %s", agent)
	}
}

func TestScaffoldCreatesWorkflows(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	planning, err := fsys.ReadFile(".borg/workflows/planning.yaml")
	assert.NoError(t, err)
	assert.NotEmpty(t, planning, "planning.yaml should be non-empty")

	assimilation, err := fsys.ReadFile(".borg/workflows/assimilation.yaml")
	assert.NoError(t, err)
	assert.NotEmpty(t, assimilation, "assimilation.yaml should be non-empty")
}

func TestScaffoldSeedsModelsYAML(t *testing.T) {
	fsys := specio.NewMemFS()
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	data, err := fsys.ReadFile(".borg/models.yaml")
	assert.NoError(t, err, ".borg/models.yaml should be seeded by init so users can edit per-project model preferences")
	assert.NotEmpty(t, data)
	// The seeded content must match the embedded source of truth byte-for-byte.
	assert.Equal(t, agent.EmbeddedModelsYAML(), data)
}

func TestResetOverwritesEmbeddedArtifacts(t *testing.T) {
	// User has scaffolded a project, then edited an agent .md file.
	// `update --reset` should overwrite that edit with the binary's
	// embedded version of the same agent.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Locally modify spec_architect.md so we can detect overwrite.
	const localEdit = "# LOCAL EDIT — should be overwritten by Reset\n"
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_architect.md", []byte(localEdit), 0o644))
	got, err := fsys.ReadFile(".borg/agents/spec_architect.md")
	require.NoError(t, err)
	require.Equal(t, localEdit, string(got), "precondition: local edit was written")

	report, err := scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err = fsys.ReadFile(".borg/agents/spec_architect.md")
	require.NoError(t, err)
	assert.NotEqual(t, localEdit, string(got),
		"Reset should have overwritten the local edit with the embedded version")
	assert.Contains(t, string(got), "spec_architect",
		"the new content should be the embedded spec_architect.md (frontmatter mentions its id)")

	// Report should list the agent files and workflow files reset.
	assert.NotEmpty(t, report.AgentsReset, "report should record reset agent files")
	assert.Contains(t, report.AgentsReset, ".borg/agents/spec_architect.md")
	assert.NotEmpty(t, report.WorkflowsReset, "report should record reset workflow files")
	assert.True(t, report.ModelsReset, "report should record models.yaml refresh")
}

func TestResetLeavesUserContentAlone(t *testing.T) {
	// Reset must NOT touch GOALS.md, .borg/spec/, .borg/history/,
	// .borg/manifest.json, .locutus/. User content survives the
	// refresh untouched.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))

	// Plant user content under each preserved location.
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fsys.MkdirAll(".locutus/sessions", 0o755))
	require.NoError(t, fsys.WriteFile("GOALS.md", []byte("user goals"), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/spec/decisions/dec-x.json", []byte(`{"id":"dec-x"}`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/history/some-event.json", []byte(`{}`), 0o644))
	require.NoError(t, fsys.WriteFile(".locutus/sessions/session.yaml", []byte(`session_id: x`), 0o644))

	manifestBefore, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)

	_, err = scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err := fsys.ReadFile("GOALS.md")
	require.NoError(t, err)
	assert.Equal(t, "user goals", string(got), "GOALS.md must survive Reset untouched")

	got, err = fsys.ReadFile(".borg/spec/decisions/dec-x.json")
	require.NoError(t, err)
	assert.Equal(t, `{"id":"dec-x"}`, string(got), ".borg/spec/ must survive Reset untouched")

	got, err = fsys.ReadFile(".borg/history/some-event.json")
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(got), ".borg/history/ must survive Reset untouched")

	got, err = fsys.ReadFile(".locutus/sessions/session.yaml")
	require.NoError(t, err)
	assert.Equal(t, `session_id: x`, string(got), ".locutus/ runtime state must survive Reset untouched")

	manifestAfter, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)
	assert.Equal(t, manifestBefore, manifestAfter, ".borg/manifest.json must survive Reset untouched")
}

func TestResetCustomAgentNotInEmbedSurvives(t *testing.T) {
	// If the user has added a custom agent file under .borg/agents/
	// that isn't in the binary's embed, Reset should NOT delete it.
	// Reset overwrites embedded names; it doesn't prune.
	fsys := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fsys, "test-project"))
	require.NoError(t, fsys.WriteFile(".borg/agents/my_custom_agent.md", []byte("custom"), 0o644))

	_, err := scaffold.Reset(fsys)
	require.NoError(t, err)

	got, err := fsys.ReadFile(".borg/agents/my_custom_agent.md")
	require.NoError(t, err)
	assert.Equal(t, "custom", string(got),
		"a custom agent file the user added should not be deleted by Reset")
}

func TestScaffoldIdempotent(t *testing.T) {
	fsys := specio.NewMemFS()

	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	err = scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err, "second run of Scaffold should not error")
}
