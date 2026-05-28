package specio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/glorious-beard/locutus/internal/frontmatter"
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

// SavePair writes the full typed struct to basePath.json and,
// when body is non-empty, a companion basePath.md sidecar.
//
// Body semantics differ by node kind:
//   - Decisions / features / bugs: all narrative lives on the typed
//     struct (rationale, alternatives, description, acceptance
//     criteria, root_cause, fix_plan). Callers pass body="" — the
//     sidecar is not written and any pre-existing one is removed.
//   - Strategies: spec.Strategy has no Body field on the struct;
//     the strategy's prose body lives in the .md sidecar (a legacy
//     of the pre-DJ-135 council authoring path). Callers pass the
//     body string and the sidecar is preserved.
//
// Post-DJ-135 all spec mutation flows through MCP write tools,
// which means decisions/features/bugs only ever go through this
// function with empty body — so their sidecars get cleaned up
// naturally as the migration touches each node. The one-shot
// sidecar-cleanup migration ([CleanupSpecSidecars] under internal/
// migrate/) handles the residual case where a previous Locutus
// build wrote frontmatter-only sidecars that this build no longer
// emits.
//
// Approaches are NOT routed through this function — spec.Approach
// stores a load-bearing markdown body that's authored and read by
// coding agents. Approaches use SaveMarkdown / LoadMarkdown
// directly.
//
// Writes are atomic on OSFS (write to .tmp then rename); on MemFS
// the write is direct.
func SavePair[T any](fsys FS, basePath string, obj T, body string) error {
	jsonData, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("save pair marshal json: %w", err)
	}
	jsonData = append(jsonData, '\n')
	if err := AtomicWriteFile(fsys, basePath+".json", jsonData, 0o644); err != nil {
		return fmt.Errorf("save pair write json: %w", err)
	}

	if body == "" {
		// Remove any leftover sidecar from prior builds. Missing
		// file is fine — no .md to clean up.
		if err := fsys.Remove(basePath + ".md"); err != nil && !isNotExistErr(err) {
			return fmt.Errorf("save pair remove stale md: %w", err)
		}
		fireSpecWrite(basePath, false)
		return nil
	}

	hdr, err := extractHeader(obj)
	if err != nil {
		return fmt.Errorf("save pair extract header: %w", err)
	}
	mdData, err := frontmatter.Render(hdr, body)
	if err != nil {
		return fmt.Errorf("save pair render md: %w", err)
	}
	if err := AtomicWriteFile(fsys, basePath+".md", mdData, 0o644); err != nil {
		return fmt.Errorf("save pair write md: %w", err)
	}
	fireSpecWrite(basePath, false)
	return nil
}

// extractHeader marshals obj to a generic map and pulls out the id,
// title, and status fields for the frontmatter header. Used only by
// SavePair when a body string is present and a sidecar is going to
// be written.
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

// isNotExistErr matches the not-exist sentinel from both OSFS and
// MemFS, both of which wrap their errors as *fs.PathError with
// fs.ErrNotExist as the underlying cause. Required on Windows, where
// the OS error message ("The system cannot find the file specified")
// does not contain the Unix-style substrings the original string-
// match version looked for.
func isNotExistErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

