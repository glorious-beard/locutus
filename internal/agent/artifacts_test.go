package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

const taskfileContent = "version: '3'\ntasks:\n  build:\n    cmds:\n      - go build ./...\n  test:\n    cmds:\n      - go test ./...\n"

const claudeMDContent = "# Project\n\nThis project uses Go...\n"

const agentsMDContent = "# Agents\n\nUse locutus for all planning...\n"

func TestGenerateTaskfile(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: taskfileContent, Model: "test-model"},
	})
	fsys := specio.NewMemFS()

	req := ArtifactRequest{
		ProjectName: "myproject",
		Strategies: []spec.Strategy{
			{
				ID:    "s-001",
				Title: "Build Strategy",
				Commands: map[string]string{
					"build": "go build ./...",
					"test":  "go test ./...",
				},
			},
			{
				ID:    "s-002",
				Title: "Lint Strategy",
				Commands: map[string]string{
					"lint": "golangci-lint run",
				},
			},
		},
	}

	err := GenerateTaskfile(context.Background(), mock, fsys, req)
	assert.NoError(t, err)

	// Verify file was written.
	data, readErr := fsys.ReadFile("Taskfile.yml")
	assert.NoError(t, readErr)
	assert.Equal(t, taskfileContent, string(data))

	// Verify LLM was called exactly once.
	assert.Equal(t, 1, mock.CallCount())

	// Verify the LLM request included strategy commands in the messages.
	calls := mock.Calls()
	assert.Len(t, calls, 1)
	messagesText := messagesContent(calls[0].Request.Messages)
	assert.Contains(t, messagesText, "go build ./...")
	assert.Contains(t, messagesText, "go test ./...")
	assert.Contains(t, messagesText, "golangci-lint run")
}

func TestGenerateClaudeMD(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: claudeMDContent, Model: "test-model"},
	})
	fsys := specio.NewMemFS()

	req := ArtifactRequest{
		ProjectName: "myproject",
		Features: []spec.Feature{
			{ID: "f-001", Title: "Spec Graph", Description: "Persistent spec graph management"},
		},
		Decisions: []spec.Decision{
			{ID: "d-001", Title: "Use YAML for spec files", Rationale: "Human-readable and widely supported"},
		},
		GoalsBody: "Build a spec-driven project manager.",
	}

	err := GenerateClaudeMD(context.Background(), mock, fsys, req)
	assert.NoError(t, err)

	// Verify file was written with correct content.
	data, readErr := fsys.ReadFile("CLAUDE.md")
	assert.NoError(t, readErr)
	assert.Equal(t, claudeMDContent, string(data))

	// Verify LLM was called once.
	assert.Equal(t, 1, mock.CallCount())
}

func TestGenerateAgentsMD(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: agentsMDContent, Model: "test-model"},
	})
	fsys := specio.NewMemFS()

	req := ArtifactRequest{
		ProjectName: "myproject",
		Features: []spec.Feature{
			{ID: "f-001", Title: "Planning Engine"},
		},
		Strategies: []spec.Strategy{
			{ID: "s-001", Title: "Code Generation Strategy"},
		},
		GoalsBody: "Autonomous project management.",
	}

	err := GenerateAgentsMD(context.Background(), mock, fsys, req)
	assert.NoError(t, err)

	// Verify file was written with correct content.
	data, readErr := fsys.ReadFile("AGENTS.md")
	assert.NoError(t, readErr)
	assert.Equal(t, agentsMDContent, string(data))

	// Verify LLM was called once.
	assert.Equal(t, 1, mock.CallCount())
}

func TestGenerateTaskfileWithNoStrategies(t *testing.T) {
	minimalTaskfile := "version: '3'\ntasks:\n  default:\n    cmds:\n      - echo 'no tasks configured'\n"
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: minimalTaskfile, Model: "test-model"},
	})
	fsys := specio.NewMemFS()

	req := ArtifactRequest{
		ProjectName: "emptyproject",
		Strategies:  []spec.Strategy{}, // empty
	}

	err := GenerateTaskfile(context.Background(), mock, fsys, req)
	assert.NoError(t, err)

	// LLM should still be called even with no strategies.
	assert.Equal(t, 1, mock.CallCount())

	// File should still be written.
	data, readErr := fsys.ReadFile("Taskfile.yml")
	assert.NoError(t, readErr)
	assert.Equal(t, minimalTaskfile, string(data))
}

func TestGenerateArtifactLLMError(t *testing.T) {
	llmErr := fmt.Errorf("provider unavailable")
	mock := NewMockLLM(MockResponse{Err: llmErr})
	fsys := specio.NewMemFS()

	req := ArtifactRequest{
		ProjectName: "failproject",
		Strategies: []spec.Strategy{
			{ID: "s-001", Title: "Some Strategy", Commands: map[string]string{"build": "make"}},
		},
	}

	err := GenerateTaskfile(context.Background(), mock, fsys, req)
	assert.Error(t, err)
	assert.ErrorIs(t, err, llmErr)

	// No file should have been written.
	_, readErr := fsys.ReadFile("Taskfile.yml")
	assert.Error(t, readErr)
}

// messagesContent concatenates all message content strings for easy assertion.
func messagesContent(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n")
}
