package specio

import (
	"fmt"

	"github.com/chetan/locutus/internal/frontmatter"
	"gopkg.in/yaml.v3"
)

// SaveMarkdown writes obj as YAML frontmatter + body to path.
// No JSON sidecar — the .md file is the sole source of truth.
// Uses the same atomicWrite as SavePair for OS safety.
func SaveMarkdown[T any](fsys FS, path string, obj T, body string) error {
	yamlData, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("save markdown marshal: %w", err)
	}

	// Build a minimal struct that yaml.Marshal already produced; wrap in delimiters.
	// Re-use frontmatter.Render which accepts any yaml-marshalable value.
	mdData, err := frontmatter.Render(obj, body)
	if err != nil {
		return fmt.Errorf("save markdown render: %w", err)
	}
	_ = yamlData // marshaled above only to surface errors early

	if err := AtomicWriteFile(fsys, path, mdData, 0o644); err != nil {
		return fmt.Errorf("save markdown write: %w", err)
	}
	return nil
}

// LoadMarkdown reads path, parses YAML frontmatter into T, and returns the body.
func LoadMarkdown[T any](fsys FS, path string) (obj T, body string, err error) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return obj, "", fmt.Errorf("load markdown read: %w", err)
	}
	body, err = frontmatter.Parse(data, &obj)
	if err != nil {
		return obj, "", fmt.Errorf("load markdown parse: %w", err)
	}
	return obj, body, nil
}
