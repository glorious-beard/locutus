package state

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/glorious-beard/locutus/internal/spec"
)

// BodyGetter is the minimal interface ComputeSpecHashes needs to
// fetch spec node body bytes for hashing. The MCP daemon's SpecStore
// satisfies this via a small adapter (see internal/agent — wired in
// the daemon's NewSpecServer constructor).
type BodyGetter interface {
	BodyBytes(id string) ([]byte, bool)
}

// ComputeSpecHashes returns the one-hop upstream subgraph hash map
// for an approach per DJ-149: approach.id + approach.parent_id +
// each entry in approach.decisions[] + approach.advances[] +
// approach.respects[]. Each value is sha256:<hex> of the spec node's
// body bytes (as returned by the getter). Map construction is
// deterministic; order independence is verified in the package test.
//
// Empty id strings (e.g., ParentID == "") are skipped silently.
// Duplicate ids (rare but possible if an approach cites the same id
// in multiple slots) collapse to a single map entry.
//
// Returns an error if any cited id is missing from the manifest
// (caller — typically the state_record_reconciliation handler — is
// expected to surface this as a state-recording failure; missing
// citations indicate the approach references a deleted spec node and
// should be classified as orphan via spec_mark_approach_drifted
// before the next reconciliation attempt).
func ComputeSpecHashes(a spec.Approach, getter BodyGetter) (map[string]string, error) {
	ids := []string{a.ID, a.ParentID}
	ids = append(ids, a.Decisions...)
	ids = append(ids, a.Advances...)
	ids = append(ids, a.Respects...)
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, alreadyHashed := out[id]; alreadyHashed {
			continue
		}
		body, ok := getter.BodyBytes(id)
		if !ok {
			return nil, fmt.Errorf("ComputeSpecHashes: spec id %q not in manifest", id)
		}
		sum := sha256.Sum256(body)
		out[id] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return out, nil
}
