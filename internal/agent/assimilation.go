package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// FileEntry represents a file in the codebase inventory.
type FileEntry struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// AssimilationRequest holds inputs for the assimilation analysis pipeline.
type AssimilationRequest struct {
	Inventory []FileEntry
}

// AssimilationResult holds the full output of assimilation analysis.
type AssimilationResult struct {
	Features   []spec.Feature
	Decisions  []spec.Decision
	Strategies []spec.Strategy
	Approaches []spec.Approach
	Entities   []spec.Entity
	Gaps       []Gap
}

// Gap represents a detected gap in the codebase.
type Gap struct {
	Category    string   `json:"category"`
	Severity    string   `json:"severity"`
	Description string   `json:"description"`
	AffectedIDs []string `json:"affected_ids,omitempty"`
}

// WalkInventory produces a file inventory from the given FS, respecting .gitignore.
func WalkInventory(fsys specio.FS) ([]FileEntry, error) {
	// Collect all file paths.
	var allFiles []string
	if mfs, ok := fsys.(*specio.MemFS); ok {
		allFiles = mfs.AllFiles()
	} else if osfs, ok := fsys.(*specio.OSFS); ok {
		err := filepath.WalkDir(osfs.Base(), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == ".borg") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(osfs.Base(), p)
				allFiles = append(allFiles, rel)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking directory: %w", err)
		}
	} else {
		return nil, fmt.Errorf("WalkInventory: unsupported FS type %T", fsys)
	}

	if len(allFiles) == 0 {
		return nil, nil
	}

	// Parse .gitignore patterns.
	ignorePatterns := parseGitignore(fsys)

	var entries []FileEntry
	for _, fp := range allFiles {
		if isIgnored(fp, ignorePatterns) {
			continue
		}

		info, err := fsys.Stat(fp)
		if err != nil {
			continue
		}

		entries = append(entries, FileEntry{
			Path:  fp,
			Size:  info.Size(),
			IsDir: info.IsDir(),
		})
	}

	return entries, nil
}

// gitignorePattern represents a single parsed .gitignore pattern.
type gitignorePattern struct {
	pattern string
	isDir   bool // pattern ends with "/"
}

// parseGitignore reads .gitignore from the FS and returns parsed patterns.
func parseGitignore(fsys specio.FS) []gitignorePattern {
	data, err := fsys.ReadFile(".gitignore")
	if err != nil {
		return nil
	}

	var patterns []gitignorePattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := gitignorePattern{pattern: line}
		if strings.HasSuffix(line, "/") {
			p.isDir = true
			p.pattern = strings.TrimSuffix(line, "/")
		}
		patterns = append(patterns, p)
	}
	return patterns
}

// isIgnored checks whether a file path matches any gitignore pattern.
func isIgnored(filePath string, patterns []gitignorePattern) bool {
	for _, p := range patterns {
		if p.isDir {
			// Directory pattern: match if path starts with the dir prefix.
			prefix := p.pattern + "/"
			if strings.HasPrefix(filePath, prefix) || filePath == p.pattern {
				return true
			}
		} else {
			// File pattern: match exact path or base name.
			if filePath == p.pattern || path.Base(filePath) == p.pattern {
				return true
			}
		}
	}
	return false
}

// Analyze runs the full assimilation pipeline using the assimilation council workflow.
func Analyze(ctx context.Context, llm LLM, fsys specio.FS, req AssimilationRequest) (*AssimilationResult, error) {
	const agentsDir = ".borg/agents"
	const workflowPath = ".borg/workflows/assimilation.yaml"

	// Load agent definitions.
	agentList, err := LoadAgentDefs(fsys, agentsDir)
	if err != nil {
		return nil, fmt.Errorf("loading assimilation agents: %w", err)
	}

	agentDefs := make(map[string]AgentDef, len(agentList))
	for _, a := range agentList {
		agentDefs[a.ID] = a
	}

	// Load workflow.
	wf, err := LoadWorkflow(fsys, workflowPath)
	if err != nil {
		return nil, fmt.Errorf("loading assimilation workflow: %w", err)
	}

	// Build the initial prompt with inventory context.
	inventoryJSON, err := json.Marshal(req.Inventory)
	if err != nil {
		return nil, fmt.Errorf("marshaling inventory: %w", err)
	}

	prompt := fmt.Sprintf("Analyze this codebase. File inventory:\n%s", string(inventoryJSON))

	// Execute the workflow.
	exec := &WorkflowExecutor{
		LLM:       llm,
		AgentDefs: agentDefs,
		Workflow:  wf,
	}

	results, err := exec.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("assimilation workflow execution: %w", err)
	}

	// Parse results into AssimilationResult.
	return parseAssimilationResults(results)
}

// parseAssimilationResults aggregates all round results into a single AssimilationResult.
func parseAssimilationResults(results []RoundResult) (*AssimilationResult, error) {
	br := &AssimilationResult{}

	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}

		// Try to parse the output as JSON and extract known fields.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.Output), &raw); err != nil {
			// Not JSON — skip.
			continue
		}

		if data, ok := raw["features"]; ok {
			var features []spec.Feature
			if err := json.Unmarshal(data, &features); err == nil {
				br.Features = append(br.Features, features...)
			}
		}

		if data, ok := raw["decisions"]; ok {
			var decisions []spec.Decision
			if err := json.Unmarshal(data, &decisions); err == nil {
				br.Decisions = append(br.Decisions, decisions...)
			}
		}

		if data, ok := raw["strategies"]; ok {
			var strategies []spec.Strategy
			if err := json.Unmarshal(data, &strategies); err == nil {
				br.Strategies = append(br.Strategies, strategies...)
			}
		}

		if data, ok := raw["approaches"]; ok {
			var approaches []spec.Approach
			if err := json.Unmarshal(data, &approaches); err == nil {
				br.Approaches = append(br.Approaches, approaches...)
			}
		}

		if data, ok := raw["entities"]; ok {
			var entities []spec.Entity
			if err := json.Unmarshal(data, &entities); err == nil {
				br.Entities = append(br.Entities, entities...)
			}
		}

		if data, ok := raw["gaps"]; ok {
			var gaps []Gap
			if err := json.Unmarshal(data, &gaps); err == nil {
				br.Gaps = append(br.Gaps, gaps...)
			}
		}
	}

	return br, nil
}
