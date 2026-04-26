package specio

import (
	"encoding/json"
	"fmt"

	"github.com/chetan/locutus/internal/frontmatter"
)

// FrontmatterHeader is the minimal set of fields written to .md frontmatter.
// The full typed struct lives in the .json sidecar; the .md is a human-friendly
// projection.
type FrontmatterHeader struct {
	ID     string `yaml:"id"     json:"id"`
	Title  string `yaml:"title"  json:"title"`
	Status string `yaml:"status" json:"status"`
}

// LoadPair reads a basePath.json file (unmarshalled into T) and a basePath.md
// file (parsed for frontmatter). The JSON file is the source of truth for the
// typed object; the markdown body is returned separately.
func LoadPair[T any](fsys FS, basePath string) (obj T, body string, err error) {
	jsonData, err := fsys.ReadFile(basePath + ".json")
	if err != nil {
		return obj, "", fmt.Errorf("load pair json: %w", err)
	}
	if err := json.Unmarshal(jsonData, &obj); err != nil {
		return obj, "", fmt.Errorf("load pair unmarshal: %w", err)
	}

	mdData, err := fsys.ReadFile(basePath + ".md")
	if err != nil {
		// Missing .md is not fatal — the JSON is the source of truth.
		return obj, "", nil
	}

	var hdr FrontmatterHeader
	body, err = frontmatter.Parse(mdData, &hdr)
	if err != nil {
		return obj, "", fmt.Errorf("load pair frontmatter: %w", err)
	}
	return obj, body, nil
}

// SavePair writes the full typed struct to basePath.json and a human-friendly
// markdown file to basePath.md with a minimal frontmatter header (id, title,
// status). Writes are atomic on OSFS (write to .tmp then rename); on MemFS the
// write is direct.
func SavePair[T any](fsys FS, basePath string, obj T, body string) error {
	// Marshal the full struct to JSON.
	jsonData, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("save pair marshal json: %w", err)
	}
	jsonData = append(jsonData, '\n')

	// Extract id/title/status from the struct via a map intermediary.
	hdr, err := extractHeader(obj)
	if err != nil {
		return fmt.Errorf("save pair extract header: %w", err)
	}

	// Render the markdown with frontmatter.
	mdData, err := frontmatter.Render(hdr, body)
	if err != nil {
		return fmt.Errorf("save pair render md: %w", err)
	}

	// Write JSON first, then MD.
	if err := AtomicWriteFile(fsys, basePath+".json", jsonData, 0o644); err != nil {
		return fmt.Errorf("save pair write json: %w", err)
	}
	if err := AtomicWriteFile(fsys, basePath+".md", mdData, 0o644); err != nil {
		return fmt.Errorf("save pair write md: %w", err)
	}
	return nil
}

// extractHeader marshals obj to a generic map and pulls out the id, title, and
// status fields for the frontmatter header.
func extractHeader(obj any) (FrontmatterHeader, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return FrontmatterHeader{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return FrontmatterHeader{}, err
	}

	str := func(key string) string {
		v, _ := m[key].(string)
		return v
	}
	return FrontmatterHeader{
		ID:     str("id"),
		Title:  str("title"),
		Status: str("status"),
	}, nil
}

