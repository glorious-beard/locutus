package spec

import (
	"crypto/sha256"
	"encoding/hex"
)

// ComputeGoalsMdHash returns a stable SHA-256 of the GOALS.md content
// in the canonical "sha256:<hex>" form used elsewhere in the codebase
// (see ComputeSpecHash for approaches and the artifact-hash helpers).
//
// The hash drives the short-circuit signal on Manifest.GoalsMdHash:
// the Phase 6 `refine goals` playbook compares hash(GOALS.md_now)
// against the persisted manifest value to decide whether the goal-
// layer sync runs or is skipped (DJ-139). Determinism on the same
// input bytes is the load-bearing property — without it the playbook
// re-runs the sync on every invocation.
//
// The "sha256:" prefix is the stable convention so a future hash-algo
// change can extend without breaking on-disk manifests; consumers
// parse on the prefix rather than assuming a particular hex length.
func ComputeGoalsMdHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
