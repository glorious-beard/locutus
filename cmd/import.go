package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ImportCmd admits a feature or bug from a markdown issue file. The
// admission is gated by GOALS.md-based triage (DJ-068 / DJ-071 style);
// --skip-triage bypasses the gate, --dry-run reports the verdict without
// writing a spec node.
type ImportCmd struct {
	Input       string `help:"Path to markdown issue file." type:"existingfile"`
	Type        string `help:"Admit as 'feature' (default) or 'bug'." default:"feature" enum:"feature,bug"`
	SkipTriage  bool   `help:"Skip GOALS.md triage evaluation; admit directly."`
	DryRun      bool   `help:"Preview triage and intended destination; do not write."`
}

// ImportResult summarises an import attempt.
type ImportResult struct {
	Accepted    bool                  `json:"accepted"`
	Verdict     *agent.TriageVerdict  `json:"verdict,omitempty"`
	Destination string                `json:"destination,omitempty"`
	FeatureID   string                `json:"feature_id,omitempty"`
	BugID       string                `json:"bug_id,omitempty"`
	DryRun      bool                  `json:"dry_run"`
	SkippedTriage bool                `json:"skipped_triage"`
}

func (c *ImportCmd) Run(ctx context.Context, cli *CLI) error {
	if c.Input == "" {
		return fmt.Errorf("--input is required")
	}
	data, err := os.ReadFile(c.Input)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	result, err := RunImport(ctx, fsys, data, c.Type, c.SkipTriage, c.DryRun)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printImportResult(result)
	if !result.Accepted {
		return ExitCode(1)
	}
	return nil
}

// RunImport is the shared implementation used by both the CLI and MCP
// handlers. It evaluates triage (unless skipped), then — if accepted and not
// dry-running — writes the spec node.
func RunImport(ctx context.Context, fsys specio.FS, data []byte, kind string, skipTriage, dryRun bool) (*ImportResult, error) {
	result := &ImportResult{DryRun: dryRun, SkippedTriage: skipTriage}

	if !skipTriage {
		goalsBody, found := readGoals(fsys)
		if found {
			llm, err := getLLM()
			if err != nil {
				return nil, err
			}
			verdict, err := agent.EvaluateAgainstGoals(ctx, llm, goalsBody, string(data))
			if err != nil {
				return nil, fmt.Errorf("triage evaluation: %w", err)
			}
			result.Verdict = verdict
			if !verdict.Accepted || verdict.Duplicate {
				result.Accepted = false
				return result, nil
			}
		}
	}

	result.Accepted = true

	switch kind {
	case "bug":
		if dryRun {
			id, dest, err := previewBugDestination(data)
			if err != nil {
				return nil, err
			}
			result.BugID = id
			result.Destination = dest
			return result, nil
		}
		bug, err := ImportBug(fsys, data)
		if err != nil {
			return nil, err
		}
		result.BugID = bug.ID
		result.Destination = ".borg/spec/bugs/" + bug.ID
	default:
		if dryRun {
			id, dest, err := previewFeatureDestination(data)
			if err != nil {
				return nil, err
			}
			result.FeatureID = id
			result.Destination = dest
			return result, nil
		}
		feat, err := ImportFeature(fsys, data)
		if err != nil {
			return nil, err
		}
		result.FeatureID = feat.ID
		result.Destination = ".borg/spec/features/" + feat.ID
	}
	return result, nil
}

func printImportResult(r *ImportResult) {
	if r.Verdict != nil {
		status := "rejected"
		switch {
		case r.Verdict.Duplicate:
			status = fmt.Sprintf("duplicate of %s", r.Verdict.DuplicateOf)
		case r.Verdict.Accepted:
			status = "accepted"
		}
		fmt.Printf("Triage verdict: %s\n", status)
		if r.Verdict.Reason != "" {
			fmt.Printf("Reason: %s\n", r.Verdict.Reason)
		}
	} else if r.SkippedTriage {
		fmt.Println("Triage: skipped.")
	} else {
		fmt.Println("Triage: GOALS.md not found; admitted without evaluation.")
	}
	if !r.Accepted {
		return
	}
	if r.DryRun {
		fmt.Printf("Dry-run: would create %s at %s.\n", nonempty(r.FeatureID, r.BugID), r.Destination)
		return
	}
	fmt.Printf("Imported %s at %s.\n", nonempty(r.FeatureID, r.BugID), r.Destination)
}

func nonempty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readGoals returns the body of GOALS.md and whether one was found.
// Looks at .borg/GOALS.md (scaffolded location) and GOALS.md (root).
func readGoals(fsys specio.FS) (string, bool) {
	if data, err := fsys.ReadFile(".borg/GOALS.md"); err == nil {
		return string(data), true
	}
	if data, err := fsys.ReadFile("GOALS.md"); err == nil {
		return string(data), true
	}
	return "", false
}

// previewFeatureDestination parses frontmatter and returns the intended
// feature ID and destination path without writing.
func previewFeatureDestination(input []byte) (string, string, error) {
	var hdr struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
	}
	if _, err := frontmatter.Parse(input, &hdr); err != nil {
		return "", "", err
	}
	if hdr.ID == "" {
		return "", "", fmt.Errorf("missing id in frontmatter")
	}
	return hdr.ID, ".borg/spec/features/" + hdr.ID, nil
}

// previewBugDestination parses frontmatter and returns the intended bug ID
// and destination path without writing.
func previewBugDestination(input []byte) (string, string, error) {
	var hdr struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
	}
	if _, err := frontmatter.Parse(input, &hdr); err != nil {
		return "", "", err
	}
	if hdr.ID == "" {
		return "", "", fmt.Errorf("missing id in frontmatter")
	}
	return hdr.ID, ".borg/spec/bugs/" + hdr.ID, nil
}

// ImportFeature reads a markdown file with YAML frontmatter, creates a Feature
// in .borg/spec/features/ as a JSON+MD pair.
func ImportFeature(fsys specio.FS, input []byte) (*spec.Feature, error) {
	var hdr struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
		Type  string `yaml:"type"`
	}

	body, err := frontmatter.Parse(input, &hdr)
	if err != nil {
		return nil, err
	}

	if hdr.ID == "" {
		return nil, fmt.Errorf("missing id in frontmatter")
	}

	now := time.Now()
	feat := spec.Feature{
		ID:          hdr.ID,
		Title:       hdr.Title,
		Status:      spec.FeatureStatusProposed,
		Description: body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := specio.SavePair(fsys, ".borg/spec/features/"+hdr.ID, feat, body); err != nil {
		return nil, err
	}

	return &feat, nil
}

// ImportBug reads a markdown file with YAML frontmatter, creates a Bug
// in .borg/spec/bugs/ as a JSON+MD pair.
func ImportBug(fsys specio.FS, input []byte) (*spec.Bug, error) {
	var hdr struct {
		ID        string `yaml:"id"`
		Title     string `yaml:"title"`
		Type      string `yaml:"type"`
		Severity  string `yaml:"severity"`
		FeatureID string `yaml:"feature_id"`
	}

	body, err := frontmatter.Parse(input, &hdr)
	if err != nil {
		return nil, err
	}

	if hdr.ID == "" {
		return nil, fmt.Errorf("missing id in frontmatter")
	}

	now := time.Now()
	bug := spec.Bug{
		ID:          hdr.ID,
		Title:       hdr.Title,
		FeatureID:   hdr.FeatureID,
		Severity:    spec.BugSeverity(hdr.Severity),
		Status:      spec.BugStatusReported,
		Description: body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := specio.SavePair(fsys, ".borg/spec/bugs/"+hdr.ID, bug, body); err != nil {
		return nil, err
	}

	return &bug, nil
}
