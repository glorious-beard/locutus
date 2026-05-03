// Package agent — spec_lookup tool surface (Phase 3 of
// council-tools-and-revise-fanout).
//
// Two Genkit tools register against the runtime so the spec_reconciler
// agent can navigate the persisted spec lazily instead of receiving
// the entire ExistingSpec snapshot inlined into its prompt:
//
//   - spec_list_manifest() — returns a compact index of every
//     persisted spec node (id, title, kind for strategies,
//     one-line summary).
//   - spec_get(id) — returns the full JSON content of one spec node,
//     identified by ID prefix.
//
// Both are pure reads against specio.FS rooted at the project. No
// persisted manifest file — `.borg/spec/` IS the manifest per DJ-068;
// the index is computed on-demand from the directory listing so there
// is no sync surface to maintain. `.borg/manifest.json` stays the
// project-root marker per DJ-081 and is not touched here.
//
// Tool granularity matters: the manifest carries enough one-line
// context (title + summary) that the reconciler can decide to fetch
// full content or skip without burning a `spec_get` call per node.

package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SpecManifest is the index returned by spec_list_manifest. Entries are
// grouped by kind so the model can scan the whole index at a glance and
// drill into a specific category without cross-array filtering.
type SpecManifest struct {
	Features   []SpecManifestEntry `json:"features,omitempty"`
	Strategies []SpecManifestEntry `json:"strategies,omitempty"`
	Decisions  []SpecManifestEntry `json:"decisions,omitempty"`
	Bugs       []SpecManifestEntry `json:"bugs,omitempty"`
	Approaches []SpecManifestEntry `json:"approaches,omitempty"`
}

// SpecManifestEntry is one node's index entry. Summary is a single-line
// truncation of the description / body / rationale — kept short so the
// manifest stays scannable but long enough that the reconciler can
// usually decide reuse vs. mint-new without a follow-up spec_get.
type SpecManifestEntry struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind,omitempty"`    // strategies only
	Summary string `json:"summary,omitempty"` // truncated to ~200 chars
}

// summaryMaxRunes caps the per-entry summary length. ~200 chars keeps
// the full manifest comfortably under a few KB even with 100 nodes
// (200 chars × 100 ≈ 20 KB), which is the whole point — the manifest
// must be cheap to scan in one tool call.
const summaryMaxRunes = 200

// BuildSpecManifest walks `.borg/spec/` and returns the index. Pure
// reads; missing kind directories (greenfield) yield empty arrays
// rather than errors. Malformed individual files are skipped with a
// slog.Warn — one bad file shouldn't poison the whole manifest.
func BuildSpecManifest(fsys specio.FS) SpecManifest {
	var m SpecManifest

	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed feature", "path", p.Path, "error", p.Err)
				continue
			}
			m.Features = append(m.Features, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: truncate(p.Object.Description, summaryMaxRunes),
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed strategy", "path", p.Path, "error", p.Err)
				continue
			}
			m.Strategies = append(m.Strategies, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Kind:    string(p.Object.Kind),
				Summary: truncate(p.Body, summaryMaxRunes),
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed decision", "path", p.Path, "error", p.Err)
				continue
			}
			m.Decisions = append(m.Decisions, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: truncate(p.Object.Rationale, summaryMaxRunes),
			})
		}
	}
	if pairs, err := specio.WalkPairs[spec.Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("spec manifest: skipping malformed bug", "path", p.Path, "error", p.Err)
				continue
			}
			m.Bugs = append(m.Bugs, SpecManifestEntry{
				ID:      p.Object.ID,
				Title:   p.Object.Title,
				Summary: truncate(p.Object.Description, summaryMaxRunes),
			})
		}
	}
	// Approaches are pure markdown (no JSON sidecar).
	if files, err := fsys.ListDir(".borg/spec/approaches"); err == nil {
		for _, f := range files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			obj, body, err := specio.LoadMarkdown[spec.Approach](fsys, f)
			if err != nil {
				slog.Warn("spec manifest: skipping malformed approach", "path", f, "error", err)
				continue
			}
			m.Approaches = append(m.Approaches, SpecManifestEntry{
				ID:      obj.ID,
				Title:   obj.Title,
				Summary: truncate(body, summaryMaxRunes),
			})
		}
	}

	return m
}

// LookupSpecNode returns the raw JSON of one spec node by id. The kind
// is inferred from the id prefix (`feat-`, `strat-`, `dec-`, `bug-`,
// `app-`); unknown prefixes return ErrSpecKindUnknown. A missing file
// returns the underlying os.ErrNotExist (or MemFS equivalent) so
// callers can distinguish "no such id" from "id with invalid prefix."
//
// For approach nodes (markdown only, no JSON sidecar), returns the
// full markdown body wrapped as a JSON string so the tool's contract
// stays "JSON in, JSON out."
func LookupSpecNode(fsys specio.FS, id string) (json.RawMessage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("spec_get: empty id")
	}
	switch {
	case strings.HasPrefix(id, "feat-"):
		return readJSON(fsys, ".borg/spec/features/"+id+".json")
	case strings.HasPrefix(id, "strat-"):
		return readJSON(fsys, ".borg/spec/strategies/"+id+".json")
	case strings.HasPrefix(id, "dec-"):
		return readJSON(fsys, ".borg/spec/decisions/"+id+".json")
	case strings.HasPrefix(id, "bug-"):
		return readJSON(fsys, ".borg/spec/bugs/"+id+".json")
	case strings.HasPrefix(id, "app-"):
		body, err := fsys.ReadFile(".borg/spec/approaches/" + id + ".md")
		if err != nil {
			return nil, err
		}
		// Wrap as JSON string so the tool's output type stays uniform.
		// Approach files have no JSON sidecar; the body is the
		// authoritative content.
		out, mErr := json.Marshal(string(body))
		if mErr != nil {
			return nil, mErr
		}
		return out, nil
	default:
		return nil, fmt.Errorf("spec_get: id %q has unknown prefix (want feat-, strat-, dec-, bug-, or app-)", id)
	}
}

func readJSON(fsys specio.FS, p string) (json.RawMessage, error) {
	data, err := fsys.ReadFile(p)
	if err != nil {
		return nil, err
	}
	// Validate it parses — return the raw bytes either way, but a
	// validation error gives the tool consumer a clear signal that
	// the file on disk is corrupt.
	if !json.Valid(data) {
		return nil, fmt.Errorf("spec_get: %s contains invalid JSON", path.Base(p))
	}
	return data, nil
}

// truncate trims s to maxRunes, appending "…" when truncated. Operates
// on runes so multibyte characters don't get sliced mid-codepoint.
// Newlines collapse to single spaces so the manifest entries stay
// one-line — multi-paragraph descriptions would defeat the point of a
// scannable index.
func truncate(s string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	return string(runes[:maxRunes]) + "…"
}

// SpecGetInput is the tool input shape for spec_get. A struct (not a
// bare string) so the tool's JSON-schema is a stable object shape the
// model can target.
type SpecGetInput struct {
	ID string `json:"id"`
}

// Spec-tool names exported so frontmatter parsers and tests can
// reference them without literal-string drift.
const (
	ToolNameSpecListManifest = "spec_list_manifest"
	ToolNameSpecGet          = "spec_get"
)

// RegisterSpecTools registers spec_list_manifest and spec_get against
// the Genkit runtime. fsys is captured by closure so tool calls read
// from the same filesystem the rest of Locutus operates on (OSFS in
// production, MemFS in tests).
//
// Idempotent only at the package-init level — Genkit's DefineTool
// panics on duplicate registration, so callers MUST invoke this once
// per *genkit.Genkit instance. The current call site is cmd/llm.go
// after NewGenKitLLM, which itself runs once per process via
// initOnce.
func RegisterSpecTools(g *genkit.Genkit, fsys specio.FS) {
	if g == nil || fsys == nil {
		return
	}
	genkit.DefineTool(g, ToolNameSpecListManifest,
		"Returns a compact index of every persisted spec node grouped by kind (features, strategies, decisions, bugs, approaches). Each entry carries id, title, optional kind (strategies), and a one-line summary truncated to ~200 chars. Use this to navigate the existing spec without dumping every node's full content.",
		func(ctx *ai.ToolContext, _ struct{}) (SpecManifest, error) {
			return BuildSpecManifest(fsys), nil
		},
	)
	genkit.DefineTool(g, ToolNameSpecGet,
		"Returns the full JSON of one spec node by id. The kind is inferred from the id prefix (feat-, strat-, dec-, bug-, app-). Use this AFTER spec_list_manifest narrows you to a candidate id you need to inspect in detail.",
		func(ctx *ai.ToolContext, in SpecGetInput) (json.RawMessage, error) {
			return LookupSpecNode(fsys, in.ID)
		},
	)
}
