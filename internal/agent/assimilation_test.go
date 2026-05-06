package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

// setupAssimilationFS creates a MemFS with assimilation agent config (agents + workflow)
// and a synthetic codebase for testing.
func setupAssimilationFS(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := specio.NewMemFS()

	// Create agent directory structure.
	assert.NoError(t, fs.MkdirAll(".borg/agents", 0o755))
	assert.NoError(t, fs.MkdirAll(".borg/workflows", 0o755))

	// Agent definitions — minimal frontmatter with id + role.
	agents := map[string]string{
		"scout": `---
id: scout
role: scout
---
You are a codebase scout. Analyze the file inventory and identify languages, frameworks, and project structure.`,

		"backend_analyzer": `---
id: backend_analyzer
role: analyzer
---
You are a backend analyzer. Examine backend code and infer decisions, entities, and strategies.`,

		"frontend_analyzer": `---
id: frontend_analyzer
role: analyzer
---
You are a frontend analyzer. Examine frontend code and infer decisions and strategies.`,

		"infra_analyzer": `---
id: infra_analyzer
role: analyzer
---
You are an infrastructure analyzer. Examine infra config and infer deployment decisions.`,

		"gap_analyst": `---
id: gap_analyst
role: analyst
---
You are a gap analyst. Identify missing tests, undocumented decisions, and orphan code.`,

		"remediator": `---
id: remediator
role: remediator
---
You are a remediator. Propose assumed decisions and features to close detected gaps.`,
	}

	for name, content := range agents {
		path := ".borg/agents/" + name + ".md"
		assert.NoError(t, fs.WriteFile(path, []byte(content), 0o644))
	}

	// Assimilation workflow.
	workflowContent := `rounds:
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
`
	assert.NoError(t, fs.WriteFile(".borg/workflows/assimilation.yaml", []byte(workflowContent), 0o644))

	// Synthetic codebase files.
	assert.NoError(t, fs.MkdirAll("internal/auth", 0o755))
	assert.NoError(t, fs.WriteFile("go.mod", []byte("module example.com/app\n\ngo 1.22\n"), 0o644))
	assert.NoError(t, fs.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644))
	assert.NoError(t, fs.WriteFile("internal/auth/handler.go", []byte("package auth\n\nfunc HandleLogin() {}\n"), 0o644))
	assert.NoError(t, fs.WriteFile("docker-compose.yml", []byte("version: '3'\nservices:\n  app:\n    build: .\n"), 0o644))

	return fs
}

func TestWalkInventory(t *testing.T) {
	fs := specio.NewMemFS()

	// Create directory structure.
	assert.NoError(t, fs.MkdirAll("internal/auth", 0o755))
	assert.NoError(t, fs.MkdirAll("dist", 0o755))

	// Non-ignored files.
	assert.NoError(t, fs.WriteFile("go.mod", []byte("module example.com/app\n\ngo 1.22\n"), 0o644))
	assert.NoError(t, fs.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644))
	assert.NoError(t, fs.WriteFile("internal/auth/handler.go", []byte("package auth\n\nfunc HandleLogin() {}\n"), 0o644))
	assert.NoError(t, fs.WriteFile("internal/auth/handler_test.go", []byte("package auth\n\nfunc TestHandleLogin() {}\n"), 0o644))
	assert.NoError(t, fs.WriteFile("docker-compose.yml", []byte("version: '3'\n"), 0o644))
	assert.NoError(t, fs.WriteFile("Taskfile.yml", []byte("version: '3'\n"), 0o644))

	// .gitignore — dist/ and .env should be excluded.
	assert.NoError(t, fs.WriteFile(".gitignore", []byte("dist/\n.env\n"), 0o644))

	// Files that should be excluded by .gitignore.
	assert.NoError(t, fs.WriteFile("dist/binary", []byte("ELF binary content here"), 0o644))
	assert.NoError(t, fs.WriteFile(".env", []byte("SECRET=hunter2\n"), 0o644))

	entries, err := WalkInventory(fs)
	assert.NoError(t, err)

	// Collect paths from entries.
	paths := make(map[string]bool)
	for _, e := range entries {
		paths[e.Path] = true
	}

	// Should include non-ignored files.
	assert.True(t, paths["go.mod"], "expected go.mod in inventory")
	assert.True(t, paths["main.go"], "expected main.go in inventory")
	assert.True(t, paths["internal/auth/handler.go"], "expected handler.go in inventory")
	assert.True(t, paths["internal/auth/handler_test.go"], "expected handler_test.go in inventory")
	assert.True(t, paths["docker-compose.yml"], "expected docker-compose.yml in inventory")
	assert.True(t, paths["Taskfile.yml"], "expected Taskfile.yml in inventory")
	assert.True(t, paths[".gitignore"], "expected .gitignore in inventory")

	// Should NOT include gitignored files.
	assert.False(t, paths["dist/binary"], "dist/binary should be excluded by .gitignore")
	assert.False(t, paths[".env"], ".env should be excluded by .gitignore")

	// Each non-empty file should have Size > 0.
	for _, e := range entries {
		assert.Greater(t, e.Size, int64(0), "expected Size > 0 for %s", e.Path)
	}
}

func TestWalkInventoryEmpty(t *testing.T) {
	fs := specio.NewMemFS()

	entries, err := WalkInventory(fs)
	assert.NoError(t, err)
	assert.Empty(t, entries)
}

func TestAnalyzeProducesSpec(t *testing.T) {
	fs := setupAssimilationFS(t)

	// Build the file inventory from the synthetic codebase.
	inventory, err := WalkInventory(fs)
	assert.NoError(t, err)
	assert.NotEmpty(t, inventory)

	// Mock LLM responses — one per agent call in workflow order:
	// 1. scout (scan round)
	// 2. backend_analyzer (analyze round, parallel)
	// 3. frontend_analyzer (analyze round, parallel)
	// 4. infra_analyzer (analyze round, parallel)
	// 5. gap_analyst (gaps round)
	// 6. remediator (remediate round)

	scoutResp := mustJSON(t, map[string]any{
		"languages":  []string{"go"},
		"frameworks": []string{"net/http"},
		"structure":  "cmd/internal",
	})

	backendResp := mustJSON(t, map[string]any{
		"decisions": []map[string]any{
			{
				"id":         "d-backend-go",
				"title":      "Backend language is Go",
				"status":     "inferred",
				"confidence": 0.95,
				"rationale":  "go.mod and .go files indicate Go backend",
			},
		},
		"entities": []map[string]any{
			{
				"id":         "e-user",
				"name":       "User",
				"kind":       "aggregate",
				"confidence": 0.8,
			},
		},
	})

	frontendResp := mustJSON(t, map[string]any{
		"decisions":  []map[string]any{},
		"strategies": []map[string]any{},
	})

	infraResp := mustJSON(t, map[string]any{
		"decisions": []map[string]any{
			{
				"id":         "d-containerization-docker",
				"title":      "Containerization via Docker",
				"status":     "inferred",
				"confidence": 0.9,
				"rationale":  "docker-compose.yml present",
			},
		},
	})

	gapResp := mustJSON(t, map[string]any{
		"gaps": []map[string]any{
			{
				"category":     "missing_tests",
				"severity":     "high",
				"description":  "handler.go has no corresponding test coverage",
				"affected_ids": []string{"e-user"},
			},
		},
	})

	remediatorResp := mustJSON(t, map[string]any{
		"decisions": []map[string]any{
			{
				"id":         "d-assumed-testing",
				"title":      "Adopt table-driven tests",
				"status":     "assumed",
				"confidence": 0.7,
				"rationale":  "Go best practice for test coverage",
			},
		},
		"features": []map[string]any{
			{
				"id":          "f-test-coverage",
				"title":       "Add test coverage for auth handler",
				"status":      "proposed",
				"description": "Write unit tests for internal/auth/handler.go",
			},
		},
	})

	mock := NewMockExecutor(
		mockResp(scoutResp),
		mockResp(backendResp),
		mockResp(frontendResp),
		mockResp(infraResp),
		mockResp(gapResp),
		mockResp(remediatorResp),
	)

	ctx := context.Background()
	result, err := Analyze(ctx, mock, fs, AssimilationRequest{Inventory: inventory})
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// At least 2 decisions: backend Go + infra Docker (possibly more from remediator).
	assert.GreaterOrEqual(t, len(result.Decisions), 2, "expected at least 2 decisions")

	// At least 1 entity (User from backend analyzer).
	assert.GreaterOrEqual(t, len(result.Entities), 1, "expected at least 1 entity")

	// At least 1 gap (missing_tests from gap analyst).
	assert.GreaterOrEqual(t, len(result.Gaps), 1, "expected at least 1 gap")

	// At least 1 feature (remediation from remediator).
	assert.GreaterOrEqual(t, len(result.Features), 1, "expected at least 1 feature")
}

func TestAnalyzeMissingAssimilationConfig(t *testing.T) {
	// MemFS with no assimilation agents directory.
	fs := specio.NewMemFS()
	assert.NoError(t, fs.WriteFile("go.mod", []byte("module example.com/app\n"), 0o644))

	inventory := []FileEntry{{Path: "go.mod", Size: 22}}

	mock := NewMockExecutor() // no responses needed — should fail before LLM call

	ctx := context.Background()
	result, err := Analyze(ctx, mock, fs, AssimilationRequest{Inventory: inventory})
	assert.Error(t, err, "expected error when assimilation config is missing")
	assert.Nil(t, result)
}

func TestAnalyzeEmptyCodebase(t *testing.T) {
	fs := setupAssimilationFS(t)

	// Remove the synthetic codebase files — keep only assimilation config.
	_ = fs.Remove("go.mod")
	_ = fs.Remove("main.go")
	_ = fs.Remove("internal/auth/handler.go")
	_ = fs.Remove("docker-compose.yml")

	// Empty inventory.
	inventory := []FileEntry{}

	// Minimal LLM responses for an empty codebase.
	scoutResp := mustJSON(t, map[string]any{
		"languages":  []string{},
		"frameworks": []string{},
		"structure":  "empty",
	})

	emptyAnalysis := mustJSON(t, map[string]any{
		"decisions":  []map[string]any{},
		"entities":   []map[string]any{},
		"strategies": []map[string]any{},
	})

	gapResp := mustJSON(t, map[string]any{
		"gaps": []map[string]any{},
	})

	remediatorResp := mustJSON(t, map[string]any{
		"decisions": []map[string]any{},
		"features":  []map[string]any{},
	})

	mock := NewMockExecutor(
		mockResp(scoutResp),     // scout
		mockResp(emptyAnalysis), // backend_analyzer
		mockResp(emptyAnalysis), // frontend_analyzer
		mockResp(emptyAnalysis), // infra_analyzer
		mockResp(gapResp),       // gap_analyst
		mockResp(remediatorResp), // remediator
	)

	ctx := context.Background()
	result, err := Analyze(ctx, mock, fs, AssimilationRequest{Inventory: inventory})
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Empty or minimal results.
	assert.Empty(t, result.Gaps, "expected no gaps for empty codebase")
	assert.Empty(t, result.Features, "expected no features for empty codebase")
}

// mustJSON marshals v to a JSON string, failing the test on error.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	assert.NoError(t, err)
	return string(data)
}

// Verify the types compile — these are compile-time checks only.
var (
	_ = FileEntry{}
	_ = AssimilationRequest{}
	_ = AssimilationResult{
		Features:   []spec.Feature{},
		Decisions:  []spec.Decision{},
		Strategies: []spec.Strategy{},
		Entities:   []spec.Entity{},
		Gaps:       []Gap{},
	}
)
