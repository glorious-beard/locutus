package cmd

import (
	"fmt"
	"time"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ImportCmd creates a feature or bug from an issue.
type ImportCmd struct {
	Input string `help:"Path to markdown issue file." type:"existingfile"`
}

func (c *ImportCmd) Run(cli *CLI) error {
	return nil // implemented in Tier 2
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
