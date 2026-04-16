package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ArtifactRequest holds the spec state needed to generate artifacts.
type ArtifactRequest struct {
	ProjectName string
	Strategies  []spec.Strategy
	Features    []spec.Feature
	Decisions   []spec.Decision
	GoalsBody   string
}

// GenerateTaskfile produces a Taskfile.yml via LLM.
func GenerateTaskfile(ctx context.Context, llm LLM, fsys specio.FS, req ArtifactRequest) error {
	var sb strings.Builder
	sb.WriteString("Generate a Taskfile.yml for the project.\n\n")

	if len(req.Strategies) > 0 {
		sb.WriteString("Strategy commands:\n")
		for _, s := range req.Strategies {
			sb.WriteString(fmt.Sprintf("- %s (ID: %s)\n", s.Title, s.ID))
			for name, cmd := range s.Commands {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", name, cmd))
			}
		}
	} else {
		sb.WriteString("No strategies defined yet.\n")
	}

	prompt := sb.String()

	resp, err := llm.Generate(ctx, GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return fmt.Errorf("generate taskfile: %w", err)
	}

	return fsys.WriteFile("Taskfile.yml", []byte(resp.Content), 0o644)
}

// GenerateClaudeMD produces a CLAUDE.md via LLM.
func GenerateClaudeMD(ctx context.Context, llm LLM, fsys specio.FS, req ArtifactRequest) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Generate a CLAUDE.md file for the project %q.\n\n", req.ProjectName))

	if len(req.Features) > 0 {
		sb.WriteString("Features:\n")
		for _, f := range req.Features {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", f.ID, f.Title))
		}
		sb.WriteString("\n")
	}

	if len(req.Decisions) > 0 {
		sb.WriteString("Decisions:\n")
		for _, d := range req.Decisions {
			sb.WriteString(fmt.Sprintf("- %s: %s — %s\n", d.ID, d.Title, d.Rationale))
		}
		sb.WriteString("\n")
	}

	if req.GoalsBody != "" {
		sb.WriteString("Goals:\n")
		sb.WriteString(req.GoalsBody)
		sb.WriteString("\n")
	}

	resp, err := llm.Generate(ctx, GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: sb.String()},
		},
	})
	if err != nil {
		return fmt.Errorf("generate claude.md: %w", err)
	}

	return fsys.WriteFile("CLAUDE.md", []byte(resp.Content), 0o644)
}

// GenerateAgentsMD produces an AGENTS.md via LLM.
func GenerateAgentsMD(ctx context.Context, llm LLM, fsys specio.FS, req ArtifactRequest) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Generate an AGENTS.md file for the project %q.\n\n", req.ProjectName))

	if len(req.Features) > 0 {
		sb.WriteString("Features:\n")
		for _, f := range req.Features {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", f.ID, f.Title))
		}
		sb.WriteString("\n")
	}

	if len(req.Strategies) > 0 {
		sb.WriteString("Strategies:\n")
		for _, s := range req.Strategies {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", s.ID, s.Title))
		}
		sb.WriteString("\n")
	}

	if req.GoalsBody != "" {
		sb.WriteString("Goals:\n")
		sb.WriteString(req.GoalsBody)
		sb.WriteString("\n")
	}

	resp, err := llm.Generate(ctx, GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: sb.String()},
		},
	})
	if err != nil {
		return fmt.Errorf("generate agents.md: %w", err)
	}

	return fsys.WriteFile("AGENTS.md", []byte(resp.Content), 0o644)
}
