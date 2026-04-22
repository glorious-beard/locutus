package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ComputeSpecHash returns a stable SHA-256 over the canonical representation
// of an Approach. Per DJ-069, Approaches are denormalized by design: the
// Body field holds the full synthesis against parent Feature/Strategy and
// applicable Decisions at synthesis time. When an upstream Decision or
// parent changes, the cascade rewrites the parent's present-tense statement
// and re-synthesizes the Approach, producing a new Body — which is what
// changes the hash. Hashing Decision contents alongside the Approach would
// duplicate the cascade signal and conflate spec-integrity bugs (broken
// cascade) with drift (stale artifacts). The `Decisions []string` field is
// kept in the hash because the *set* of decisions consulted is Approach-
// owned metadata; the Decision node contents are not.
//
// Canonicalisation rules:
//   - String slices are sorted so frontmatter ordering doesn't affect the
//     hash.
//   - Timestamps are excluded — they change on every save and would produce
//     spurious drift signals.
//   - Skills, Prerequisites, and Assertions are included — they affect what
//     the coding agent does.
func ComputeSpecHash(a Approach) string {
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

	decRefs := append([]string(nil), a.Decisions...)
	sort.Strings(decRefs)
	artifacts := append([]string(nil), a.ArtifactPaths...)
	sort.Strings(artifacts)
	skills := append([]string(nil), a.Skills...)
	sort.Strings(skills)
	prereqs := append([]string(nil), a.Prerequisites...)
	sort.Strings(prereqs)

	p := canonicalApproach{
		ID:            a.ID,
		Title:         a.Title,
		ParentID:      a.ParentID,
		Body:          a.Body,
		ArtifactPaths: artifacts,
		Decisions:     decRefs,
		Skills:        skills,
		Prerequisites: prereqs,
		Assertions:    a.Assertions,
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

