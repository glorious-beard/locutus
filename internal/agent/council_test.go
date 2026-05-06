package agent

import (
	"os"
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentDir = ".borg/agents"

func setupAgentFS(t *testing.T) *specio.MemFS {
	t.Helper()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(agentDir, 0o755))

	planner := `---
id: planner
role: Planner
models:
  - {provider: anthropic, tier: balanced}
---
You are the planner agent. Propose features, decisions, and strategies.
`
	critic := `---
id: critic
role: Critic
models:
  - {provider: anthropic, tier: balanced}
---
You are the critic agent. Challenge assumptions and find flaws in proposals.
`
	historian := `---
id: historian
role: Historian
models:
  - {provider: anthropic, tier: fast}
---
You are the historian agent. Recall past decisions and their outcomes.
`

	require.NoError(t, fsys.WriteFile(agentDir+"/planner.md", []byte(planner), 0o644))
	require.NoError(t, fsys.WriteFile(agentDir+"/critic.md", []byte(critic), 0o644))
	require.NoError(t, fsys.WriteFile(agentDir+"/historian.md", []byte(historian), 0o644))

	return fsys
}

func TestLoadAgentDefs(t *testing.T) {
	fsys := setupAgentFS(t)

	defs, err := LoadAgentDefs(fsys, agentDir)

	require.NoError(t, err)
	require.Len(t, defs, 3)

	byID := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}

	p := byID["planner"]
	assert.Equal(t, "planner", p.ID)
	assert.Equal(t, "Planner", p.Role)
	require.Len(t, p.Models, 1)
	assert.Equal(t, "anthropic", p.Models[0].Provider)
	assert.Equal(t, "balanced", p.Models[0].Tier)
	assert.Equal(t, "You are the planner agent. Propose features, decisions, and strategies.\n", p.SystemPrompt)

	h := byID["historian"]
	assert.Equal(t, "fast", h.Models[0].Tier)
}

func TestLoadAgentDefsEmpty(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(agentDir, 0o755))

	defs, err := LoadAgentDefs(fsys, agentDir)

	require.NoError(t, err)
	assert.Empty(t, defs)
}

func TestLoadAgentDefsMissingDir(t *testing.T) {
	fsys := specio.NewMemFS()

	_, err := LoadAgentDefs(fsys, ".borg/agents")

	assert.Error(t, err)
}

func TestLoadAgentDefsParsesTimeout(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_feature_elaborator.md", []byte(`---
id: spec_feature_elaborator
role: planning
timeout: 5m
models:
  - {provider: anthropic, tier: balanced}
---
You are the elaborator.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_architect.md", []byte(`---
id: spec_architect
role: planning
models:
  - {provider: anthropic, tier: strong}
---
You are the architect.
`), 0o644))

	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	require.NoError(t, err)
	require.Len(t, defs, 2)

	byID := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}
	assert.Equal(t, "5m", byID["spec_feature_elaborator"].Timeout,
		"frontmatter `timeout: 5m` round-trips as a string field; perCallTimeout does the parse")
	assert.Empty(t, byID["spec_architect"].Timeout,
		"missing key defaults to empty; the global default applies")
}

func TestLoadAgentDefsParsesGrounding(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_scout.md", []byte(`---
id: spec_scout
role: survey
grounding: true
models:
  - {provider: googleai, tier: strong}
---
You are the scout.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_architect.md", []byte(`---
id: spec_architect
role: planning
models:
  - {provider: anthropic, tier: strong}
---
You are the architect.
`), 0o644))

	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	require.NoError(t, err)
	require.Len(t, defs, 2)

	byID := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}
	assert.True(t, byID["spec_scout"].Grounding,
		"frontmatter grounding: true must round-trip via yaml.Unmarshal")
	assert.False(t, byID["spec_architect"].Grounding,
		"missing grounding key must default to false; agents not opted-in stay ungrounded")
}

func TestBuildSystemPromptAppendsSchemaDoc(t *testing.T) {
	t.Run("no schema yields prompt unchanged", func(t *testing.T) {
		def := AgentDef{ID: "noschema", SystemPrompt: "hello"}
		out := BuildSystemPrompt(def)
		assert.Equal(t, "hello", out)
	})

	t.Run("registered schema is appended as a fenced JSON block", func(t *testing.T) {
		def := AgentDef{ID: "scout", SystemPrompt: "scout prompt", OutputSchema: "ScoutBrief"}
		out := BuildSystemPrompt(def)
		assert.Contains(t, out, "scout prompt")
		assert.Contains(t, out, "## Output JSON Schema")
		assert.Contains(t, out, "domain_read")
	})
}

func TestLoadAgentDefsSkipsNonMarkdown(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(agentDir, 0o755))

	a := `---
id: planner
role: Planner
models:
  - {provider: anthropic, tier: balanced}
---
System prompt here.
`
	require.NoError(t, fsys.WriteFile(agentDir+"/planner.md", []byte(a), os.FileMode(0o644)))
	require.NoError(t, fsys.WriteFile(agentDir+"/README.txt", []byte("not an agent"), os.FileMode(0o644)))

	defs, err := LoadAgentDefs(fsys, agentDir)

	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "planner", defs[0].ID)
}
