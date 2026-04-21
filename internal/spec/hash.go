package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ComputeSpecHash returns a stable SHA-256 over the canonical representation
// of an Approach plus the slice of decisions it was synthesized against.
// Parent metadata (the Feature or Strategy) is captured implicitly via the
// Approach's Body field, which is the LLM-synthesized brief.
//
// The hash is stored in the state store at reconcile time; a subsequent
// reconciliation compares the freshly computed hash to the stored one to
// detect forward drift (spec changed, artifacts have not).
//
// Canonicalisation rules:
//   - Decisions are sorted by ID before hashing so ordering of the
//     `decisions:` slice in the Approach frontmatter does not change the
//     hash.
//   - Timestamps are excluded (CreatedAt / UpdatedAt) — they change on every
//     save and would produce spurious drift signals.
//   - Skills and Prerequisites are included (they affect what the coding
//     agent will do).
func ComputeSpecHash(a Approach, applicable []Decision) string {
	type canonicalApproach struct {
		ID            string      `json:"id"`
		Title         string      `json:"title"`
		ParentID      string      `json:"parent_id"`
		Body          string      `json:"body"`
		ArtifactPaths []string    `json:"artifact_paths"`
		Decisions     []string    `json:"decisions"`
		Skills        []string    `json:"skills"`
		Prerequisites []string    `json:"prerequisites"`
		Assertions    []Assertion `json:"assertions"`
	}
	type canonicalDecision struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Status   string  `json:"status"`
		Rationale string `json:"rationale,omitempty"`
		Confidence float64 `json:"confidence,omitempty"`
	}
	type payload struct {
		Approach canonicalApproach    `json:"approach"`
		Decisions []canonicalDecision `json:"decisions"`
	}

	// Copy + sort decision refs on the approach itself for stable encoding.
	decRefs := append([]string(nil), a.Decisions...)
	sort.Strings(decRefs)
	artifacts := append([]string(nil), a.ArtifactPaths...)
	sort.Strings(artifacts)
	skills := append([]string(nil), a.Skills...)
	sort.Strings(skills)
	prereqs := append([]string(nil), a.Prerequisites...)
	sort.Strings(prereqs)

	// Sort supplied decisions by ID.
	sortedDecs := append([]Decision(nil), applicable...)
	sort.Slice(sortedDecs, func(i, j int) bool { return sortedDecs[i].ID < sortedDecs[j].ID })

	p := payload{
		Approach: canonicalApproach{
			ID:            a.ID,
			Title:         a.Title,
			ParentID:      a.ParentID,
			Body:          a.Body,
			ArtifactPaths: artifacts,
			Decisions:     decRefs,
			Skills:        skills,
			Prerequisites: prereqs,
			Assertions:    a.Assertions,
		},
	}
	for _, d := range sortedDecs {
		p.Decisions = append(p.Decisions, canonicalDecision{
			ID: d.ID, Title: d.Title, Status: string(d.Status), Rationale: d.Rationale, Confidence: d.Confidence,
		})
	}

	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReadFunc reads a file's contents by path. Matches the signature of
// specio.FS.ReadFile so callers can pass fsys.ReadFile directly without
// creating an import cycle between spec and specio.
type ReadFunc func(path string) ([]byte, error)

// ComputeArtifactHashes returns a map of artifact path → sha256 for every
// path in the approach. Missing files appear in the map with an empty hash
// so callers can distinguish "file is gone" from "file unchanged" from
// "file changed". The caller supplies the ReadFunc so spec has no FS
// dependency.
func ComputeArtifactHashes(read ReadFunc, approach Approach) map[string]string {
	out := make(map[string]string, len(approach.ArtifactPaths))
	for _, p := range approach.ArtifactPaths {
		data, err := read(p)
		if err != nil {
			out[p] = ""
			continue
		}
		sum := sha256.Sum256(data)
		out[p] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return out
}

// ArtifactsEqual reports whether two artifact-hash maps agree on every
// path. A nil/empty map is treated as "no artifacts yet" and matches only
// another nil/empty.
func ArtifactsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

