package cmd

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

func setupAnalyzeFS(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := specio.NewMemFS()

	// Spec directories.
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)
	fs.MkdirAll(".borg/spec/decisions", 0o755)
	fs.MkdirAll(".borg/spec/strategies", 0o755)
	fs.WriteFile(".borg/spec/traces.json", []byte(`{"entries":{}}`), 0o644)

	// Assimilation agents and workflows.
	fs.MkdirAll(".borg/agents", 0o755)
	fs.MkdirAll(".borg/workflows", 0o755)
	agents := []string{"scout", "backend_analyzer", "frontend_analyzer", "infra_analyzer", "gap_analyst", "remediator"}
	for _, id := range agents {
		content := "---\nid: " + id + "\nrole: " + id + "\n---\nYou are the " + id + ".\n"
		fs.WriteFile(".borg/agents/"+id+".md", []byte(content), 0o644)
	}

	// Assimilation workflow matching embedded workflow.yaml.
	fs.WriteFile(".borg/workflows/assimilation.yaml", []byte(`rounds:
  - id: scan
    agent: scout
    parallel: false
  - id: analyze
    agents: [backend_analyzer, frontend_analyzer, infra_analyzer]
    parallel: true
    depends_on: [scan]
  - id: gaps
    agent: gap_analyst
    parallel: false
    depends_on: [analyze]
  - id: remediate
    agent: remediator
    parallel: false
    depends_on: [gaps]
max_rounds: 1
`), 0o644)

	// Synthetic codebase.
	fs.WriteFile("go.mod", []byte("module example.com/app\ngo 1.22\n"), 0o644)
	fs.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644)

	return fs
}

func mockAssimilationLLM() *agent.MockLLM {
	return agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"languages":["go"],"frameworks":[],"structure":"single-binary"}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-go","title":"Go backend","status":"inferred","confidence":0.95}],"entities":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[],"strategies":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"gaps":[]}`}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"decisions":[{"id":"d-testing","title":"Add tests","status":"assumed"}],"features":[]}`}},
	)
}

func TestRunAnalyzeProducesSpec(t *testing.T) {
	fs := setupAnalyzeFS(t)
	llm := mockAssimilationLLM()

	result, err := RunAnalyze(context.Background(), llm, fs)
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	assert.NotEmpty(t, result.Decisions, "should produce decisions")
}

func TestRunAnalyzeMissingConfig(t *testing.T) {
	fs := specio.NewMemFS()
	llm := agent.NewMockLLM()

	result, err := RunAnalyze(context.Background(), llm, fs)
	assert.Error(t, err)
	assert.Nil(t, result)
}
