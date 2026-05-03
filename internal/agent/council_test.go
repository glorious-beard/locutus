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
	err := fsys.MkdirAll(agentDir, 0o755)
	assert.NoError(t, err)

	planner := `---
id: planner
role: Planner
model: anthropic/claude-sonnet-4-20250514
temperature: 0.7
---
You are the planner agent. Propose features, decisions, and strategies.
`
	critic := `---
id: critic
role: Critic
model: anthropic/claude-sonnet-4-20250514
temperature: 0.3
---
You are the critic agent. Challenge assumptions and find flaws in proposals.
`
	historian := `---
id: historian
role: Historian
model: anthropic/claude-haiku-4-20250514
temperature: 0.2
---
You are the historian agent. Recall past decisions and their outcomes.
`

	assert.NoError(t, fsys.WriteFile(agentDir+"/planner.md", []byte(planner), 0o644))
	assert.NoError(t, fsys.WriteFile(agentDir+"/critic.md", []byte(critic), 0o644))
	assert.NoError(t, fsys.WriteFile(agentDir+"/historian.md", []byte(historian), 0o644))

	return fsys
}

func TestLoadAgentDefs(t *testing.T) {
	fsys := setupAgentFS(t)

	defs, err := LoadAgentDefs(fsys, agentDir)

	assert.NoError(t, err)
	assert.Len(t, defs, 3)

	// ListDir returns sorted paths, so order is: critic, historian, planner.
	byID := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}

	// Planner
	p := byID["planner"]
	assert.Equal(t, "planner", p.ID)
	assert.Equal(t, "Planner", p.Role)
	assert.Equal(t, "anthropic/claude-sonnet-4-20250514", p.Model)
	assert.InDelta(t, 0.7, p.Temperature, 0.001)
	assert.Equal(t, "You are the planner agent. Propose features, decisions, and strategies.\n", p.SystemPrompt)

	// Critic
	c := byID["critic"]
	assert.Equal(t, "critic", c.ID)
	assert.Equal(t, "Critic", c.Role)
	assert.Equal(t, "anthropic/claude-sonnet-4-20250514", c.Model)
	assert.InDelta(t, 0.3, c.Temperature, 0.001)
	assert.Equal(t, "You are the critic agent. Challenge assumptions and find flaws in proposals.\n", c.SystemPrompt)

	// Historian
	h := byID["historian"]
	assert.Equal(t, "historian", h.ID)
	assert.Equal(t, "Historian", h.Role)
	assert.Equal(t, "anthropic/claude-haiku-4-20250514", h.Model)
	assert.InDelta(t, 0.2, h.Temperature, 0.001)
	assert.Equal(t, "You are the historian agent. Recall past decisions and their outcomes.\n", h.SystemPrompt)
}

func TestLoadAgentDefsEmpty(t *testing.T) {
	fsys := specio.NewMemFS()
	err := fsys.MkdirAll(agentDir, 0o755)
	assert.NoError(t, err)

	defs, err := LoadAgentDefs(fsys, agentDir)

	assert.NoError(t, err)
	assert.Empty(t, defs)
}

func TestLoadAgentDefsMissingDir(t *testing.T) {
	fsys := specio.NewMemFS()

	_, err := LoadAgentDefs(fsys, ".borg/agents")

	assert.Error(t, err)
}

func TestBuildGenerateRequest(t *testing.T) {
	def := AgentDef{
		ID:           "planner",
		Role:         "Planner",
		Model:        "anthropic/claude-sonnet-4-20250514",
		Temperature:  0.7,
		SystemPrompt: "You are the planner agent.",
	}

	messages := []Message{
		{Role: "user", Content: "What features should we build?"},
		{Role: "user", Content: "Focus on the MVP scope."},
	}

	req := BuildGenerateRequest(def, messages)

	assert.Equal(t, "anthropic/claude-sonnet-4-20250514", req.Model)
	assert.InDelta(t, 0.7, req.Temperature, 0.001)

	// First message must be the system prompt.
	assert.Len(t, req.Messages, 3)
	assert.Equal(t, "system", req.Messages[0].Role)
	assert.Equal(t, "You are the planner agent.", req.Messages[0].Content)

	// Remaining messages follow in order.
	assert.Equal(t, "user", req.Messages[1].Role)
	assert.Equal(t, "What features should we build?", req.Messages[1].Content)
	assert.Equal(t, "user", req.Messages[2].Role)
	assert.Equal(t, "Focus on the MVP scope.", req.Messages[2].Content)
}

func TestBuildGenerateRequestThreadsGrounding(t *testing.T) {
	def := AgentDef{
		ID:           "spec_scout",
		Role:         "survey",
		Model:        "googleai/gemini-2.5-pro",
		Grounding:    true,
		SystemPrompt: "You are the scout.",
	}
	req := BuildGenerateRequest(def, []Message{{Role: "user", Content: "x"}})
	assert.True(t, req.Grounding,
		"Grounding from frontmatter must surface on GenerateRequest so the provider config can attach GoogleSearch")
}

func TestLoadAgentDefsParsesGrounding(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_scout.md", []byte(`---
id: spec_scout
role: survey
capability: strong
grounding: true
---
You are the scout.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec_architect.md", []byte(`---
id: spec_architect
role: planning
capability: strong
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

func TestBuildGenerateRequestDefaultModel(t *testing.T) {
	def := AgentDef{
		ID:           "reviewer",
		Role:         "Reviewer",
		Model:        "", // empty — should get a default
		Temperature:  0.5,
		SystemPrompt: "You review code.",
	}

	messages := []Message{
		{Role: "user", Content: "Review this diff."},
	}

	req := BuildGenerateRequest(def, messages)

	// When Model is empty, a sensible default should be used.
	assert.NotEmpty(t, req.Model)
	assert.Equal(t, DefaultModel, req.Model)

	// System prompt still prepended.
	assert.Len(t, req.Messages, 2)
	assert.Equal(t, "system", req.Messages[0].Role)
	assert.Equal(t, "You review code.", req.Messages[0].Content)
}

// TestLoadAgentDefsSkipsNonMarkdown ensures non-.md files are ignored.
func TestLoadAgentDefsSkipsNonMarkdown(t *testing.T) {
	fsys := specio.NewMemFS()
	err := fsys.MkdirAll(agentDir, 0o755)
	assert.NoError(t, err)

	agent := `---
id: planner
role: Planner
---
System prompt here.
`
	assert.NoError(t, fsys.WriteFile(agentDir+"/planner.md", []byte(agent), os.FileMode(0o644)))
	assert.NoError(t, fsys.WriteFile(agentDir+"/README.txt", []byte("not an agent"), os.FileMode(0o644)))

	defs, err := LoadAgentDefs(fsys, agentDir)

	assert.NoError(t, err)
	assert.Len(t, defs, 1)
	assert.Equal(t, "planner", defs[0].ID)
}
