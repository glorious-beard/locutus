# DJ-149 `adopt` Spec → Code Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the `code_adoption` activity playbook (closes the second half of the DJ-135 phase 5 checkpoint 3 leak); re-establish per-approach state at `.borg/state/` per DJ-068/DJ-096; migrate `ReconciliationState.SpecHash string` → `SpecHashes map[string]string`; add six state MCP tools (4 write + 2 read) all captureOnly-wrapped per DJ-147; extend the overlay for state captures; partially supersede DJ-148 by reverting the state-side fields from `spec.Approach` and re-routing assimilate's reconciliation writes through the new state surface.

**Architecture:** State separation per DJ-068: `spec.Approach` describes WHAT (body, parent, citations); `.borg/state/<approach-id>.yaml` records IS (per-file artifacts, status, last_reconciled). The state surface gains map-based `SpecHashes` for granular one-hop-upstream subgraph drift detection (set diff + hash diff catches cascade-driven drift, refine revisions, renames as coincident add+remove). Adopt's master plan is runtime-decided: Locutus writes plan files to `.locutus/sessions/<sid>/plans/`; the runtime decides parallelism, branch ordering, worktree management; stacked branches `adopt/<NNN>-<approach-id>` with phase-N+1-branches-off-N; halt on first failure; test-asserted live status per DJ-068.

**Tech Stack:** Go 1.x; `github.com/modelcontextprotocol/go-sdk` v1.6.1; `github.com/coder/acp-go-sdk`; `github.com/stretchr/testify/{assert,require}`; `gopkg.in/yaml.v3`. Spec doc: [docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md](../../docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md).

---

## Required reading before starting

- **[docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md](../../docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md)** — the governing spec. Read all 10 decision points + Resolved Questions + Alternatives + Consequences.
- **[docs/decisions/dj-068-manifest-state-separation-kubernetes-inspired.md](../../docs/decisions/dj-068-manifest-state-separation-kubernetes-inspired.md)** — the manifest/state separation principle DJ-149 re-establishes; the 8-status `ReconcileStatus` model; the `Artifacts map[path]hash` shape that DJ-149 keeps.
- **[docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md](../../docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md)** — the DJ that DJ-149 partially supersedes; understand which contributions stay (playbook, agents, propose/revise approach tools) and which revert (state-side fields on spec.Approach).
- **[docs/decisions/dj-147-dry-run-mutation-capture.md](../../docs/decisions/dj-147-dry-run-mutation-capture.md)** — the captureOnly wrapper + sessionOverlay design; DJ-149's state tools mirror its pattern.
- **[docs/agent-conventions.md](../../docs/agent-conventions.md)** — MANDATORY before any agent prompt edit. Walk the six anti-patterns + four positive patterns as a checklist before Tasks 13–14.
- **[internal/scaffold/plans/code_assimilation.md](../../internal/scaffold/plans/code_assimilation.md) + [.claude-code.md](../../internal/scaffold/plans/code_assimilation.claude-code.md) + [.interactive.md](../../internal/scaffold/plans/code_assimilation.interactive.md)** — the three-tier playbook pattern DJ-149 mirrors for adopt.

## Phase ordering & independence

- **Phase 1** (revert DJ-148 state fields) is foundational; everything else builds on a clean spec.Approach.
- **Phase 2** (state schema migration + helper) is independent; can run in parallel with Phase 1.
- **Phase 3** (overlay extension) depends on Phase 2's struct definition.
- **Phase 4** (state MCP tools) depends on Phases 2 + 3.
- **Phase 5** (subagent rewrites) is independent; pure markdown edits.
- **Phase 6** (playbook authoring) depends on Phases 4 + 5.
- **Phase 7** (assimilate re-route + cmd + registry + CLI guard + docs) depends on Phases 4 + 6.
- **Phase 8** (validation) last.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:` / `fix:` / `refactor:` / `test:` / `docs:`).

---

## Phase 1 — Revert DJ-148 state fields

### Task 1: Revert `spec.Approach` SourceFiles/SourceHash/SourceHashSyncedAt

**Files:**
- Modify: `internal/spec/approach.go` (remove three fields)
- Modify: `internal/spec/approach_test.go` (remove DJ-148 SourceBinding tests)

- [ ] **Step 1: Remove the three fields from the struct**

In `internal/spec/approach.go`, delete the field block added by DJ-148 Task 2 (commit `8fb144f`). Specifically, remove lines around 56–77 — the `SourceFiles`, `SourceHash`, `SourceHashSyncedAt` declarations along with their doc comments. The remaining `Approach` struct should restore the pre-DJ-148 shape: `ID`, `Summary`, `Title`, `ParentID`, `Body`, `ArtifactPaths`, `Decisions`, `Skills`, `Prerequisites`, `Assertions`, `InvalidatedByEventID`, `Advances`, `Respects`, `CreatedAt`, `UpdatedAt`.

- [ ] **Step 2: Remove DJ-148 SourceBinding tests**

In `internal/spec/approach_test.go`, delete the two test functions `TestApproach_SourceBindingFields` and `TestApproach_SourceBindingOmittedWhenEmpty` (added by DJ-148 Task 2). Keep any other tests in the file unchanged.

- [ ] **Step 3: Verify the spec package compiles + tests pass**

Run: `cd /Users/chetan/projects/locutus && go build ./internal/spec/... && go test ./internal/spec/ -v 2>&1 | tail -10`
Expected: clean build; all remaining tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/spec/approach.go internal/spec/approach_test.go
git commit -m "refactor(spec): revert DJ-148 source-binding fields from Approach (DJ-149)"
```

---

### Task 2: Revert MCP plumbing for the DJ-148 state fields

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (revert proposeApproachInput fields, buildApproachBody validation/build, capture closures' use of fields, handler text)
- Modify: `internal/mcp/tools_spec_write_test.go` (revert TestBuildApproachBody assertions + TestProposeApproach_PersistsToDisk assertions for source_*)
- Modify: `internal/mcp/tools_spec_write_dry_run_test.go` (revert dry-run table entries' source_files/source_hash args)

- [ ] **Step 1: Remove SourceFiles + SourceHash from `proposeApproachInput`**

In `internal/mcp/tools_spec_write.go`, find `type proposeApproachInput struct` (around line 220). Delete the two field lines:
```go
SourceFiles []string `json:"source_files" jsonschema:"..."`
SourceHash  string   `json:"source_hash" jsonschema:"..."`
```
The struct should now have only: `ID`, `Title`, `Summary`, `ParentID`, `Body`, `Decisions`, `Advances`, `Respects`.

- [ ] **Step 2: Remove the corresponding fields + validation from `buildApproachBody`**

In `internal/mcp/tools_spec_write.go`, find `func buildApproachBody`. Remove:
- The `len(in.SourceFiles) == 0` check and its error
- The `hash := strings.TrimSpace(in.SourceHash)` block and its validation (the sha256:<hex> check)
- The `SourceFiles`, `SourceHash`, `SourceHashSyncedAt` fields from the returned `spec.Approach{}` literal

The returned struct should set only: `ID`, `Title`, `Summary`, `ParentID`, `Body`, `Decisions`, `Advances`, `Respects`, `CreatedAt`, `UpdatedAt`.

- [ ] **Step 3: Update the description constants to drop source_files/source_hash references**

Find `descSpecProposeApproach` and `descSpecReviseApproach`. Remove all sentences mentioning `source_files`, `source_hash`, "agent ensures every path exists", "sha256:<hex> hash over the sorted-paths-then-contents". The descriptions now describe approach bodies only (id, parent_id, body, decisions, advances, respects).

- [ ] **Step 4: Update handler success messages**

Two places (around lines 389 + 424): remove the source_hash and binds-N-files references from the success text:
```go
// before:
return textResult(fmt.Sprintf("Proposed approach %s under %s (binds %d files; source_hash %s).", body.ID, body.ParentID, len(body.SourceFiles), body.SourceHash)), nil, nil
// after:
return textResult(fmt.Sprintf("Proposed approach %s under %s.", body.ID, body.ParentID)), nil, nil
```

Same for `spec_revise_approach`'s success message.

- [ ] **Step 5: Revert TestBuildApproachBody tests**

In `internal/mcp/tools_spec_write_test.go`, delete:
- `TestBuildApproachBody_PopulatesAllFields` — drop the assertions on SourceFiles/SourceHash/SourceHashSyncedAt; if the test becomes trivial (just checks ID/Title/ParentID), keep it; if not, delete entirely.
- `TestBuildApproachBody_PreservesCreatedAtWhenSupplied` — drop SourceFiles/SourceHash from the input; assertion on CreatedAt preservation stays.
- `TestBuildApproachBody_RejectsMissingRequiredFields` — delete the table entries for "empty source_files", "empty source_hash", "malformed source_hash" (the remaining entries — empty id, wrong id prefix, empty title, empty parent_id, bad parent prefix — stay).

- [ ] **Step 6: Revert TestProposeApproach_PersistsToDisk assertions**

Same file, find `TestProposeApproach_PersistsToDisk`. Drop source_files + source_hash from the tool call's `Arguments` map; drop the four `assert.Contains(t, contents, "source_files:")` / etc. assertions. Keep the test's parent-feature-seed, the tool call shape, and the persistence verification (file exists, contains id/title/parent_id).

- [ ] **Step 7: Revert dry-run table entries**

In `internal/mcp/tools_spec_write_dry_run_test.go`, find the table for `TestDryRunCapturesProposeAndRevise` (or similar). The `propose_approach` table entry's `args` map drops `source_files` + `source_hash` (DJ-148 Task 6 added them). Same for `TestDryRunCapturesReviseApproach` if it exists as a standalone (DJ-148 Task 7) — drop the source_files/source_hash from its tool call args. The captureProposeApproach/captureReviseApproach behavior stays; just the inputs are smaller.

- [ ] **Step 8: Verify the mcp package compiles + tests pass under race**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_test.go internal/mcp/tools_spec_write_dry_run_test.go
git commit -m "refactor(mcp): revert DJ-148 source-binding fields from approach MCP surface (DJ-149)"
```

---

### Task 3: Update DJ-148 doc with partial-supersession status header

**Files:**
- Modify: `docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md` (status line only)

- [ ] **Step 1: Update the Status block**

Find the line `**Status:** settled (designed 2026-05-30; no code yet).` near the top of the doc. Replace with:

```markdown
**Status:** shipped 2026-05-30; state-side fields (`SourceFiles` / `SourceHash` / `SourceHashSyncedAt` on `spec.Approach`) **partially superseded by [DJ-149](dj-149-adopt-spec-to-code-reconciliation.md)** — state is re-established at `.borg/state/` per DJ-068's manifest/state separation, which DJ-148 inadvertently reinvented inside the spec graph. The playbook, the rewritten subagent prompts, the bidirectional-reconciliation principle, and the `spec_propose_approach` / `spec_revise_approach` MCP tools (for proposing/revising approach **bodies**, separate from state) survive intact. Body text below is preserved as-written for historical context; the actual state surface and assimilate's hash-computation block are updated per DJ-149.
```

- [ ] **Step 2: Verify the docs bijection test still passes**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/docs/`
Expected: PASS.

- [ ] **Step 3: Update DECISION_JOURNAL.md manifest row for DJ-148**

In `docs/DECISION_JOURNAL.md`, find the DJ-148 row. Change its `Status` column from `settled` to `shipped; state-side fields partially superseded by DJ-149`. Keep the title and link unchanged.

- [ ] **Step 4: Commit**

```bash
git add docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md docs/DECISION_JOURNAL.md
git commit -m "docs(dj-148): mark state-side fields as partially superseded by DJ-149"
```

---

## Phase 2 — State schema migration + computation helper

### Task 4: Migrate `ReconciliationState.SpecHash` to `SpecHashes map[string]string`

**Files:**
- Modify: `internal/state/state.go` (struct migration)
- Modify: `internal/state/state_test.go` (if exists) or `internal/state/store_test.go` (update any tests that reference SpecHash)

- [ ] **Step 1: Migrate the struct field**

In `internal/state/state.go`, find the `ReconciliationState` struct. Replace:
```go
SpecHash string `yaml:"spec_hash"`
```
with:
```go
// SpecHashes captures the one-hop upstream subgraph the approach
// was reconciled against, keyed by spec id. Includes approach.id +
// approach.parent_id + each entry in approach.decisions[] +
// approach.advances[] + approach.respects[]. Set-diff drift
// (added/removed keys — catches renames as coincident add+remove)
// AND hash-diff drift (body changed on a same-id key) both surface
// via key-by-key comparison. Per DJ-149.
SpecHashes map[string]string `yaml:"spec_hashes,omitempty"`
```

- [ ] **Step 2: Find + update any callers**

Run: `cd /Users/chetan/projects/locutus && grep -rn '\.SpecHash\b' --include='*.go'`
Expected: hits in `internal/reconcile/classify.go` and possibly tests.

For each hit, update from `.SpecHash` (string) to `.SpecHashes` (map). In `classify.go`, the existing logic compares a single spec hash; under DJ-149 the comparison needs to be per-key. For Task 4's scope, just compile-correct the references (read the map's keys, compare against current spec hashes if any). The full classify.go rewrite happens in the adopt playbook (not Go-side); the existing function can be left as a stub or removed if it's unused outside tests. Verify usage:

Run: `cd /Users/chetan/projects/locutus && grep -rn 'classify\.' --include='*.go'`

If `classify.go`'s functions are only test-referenced, simplify: convert to a documented stub or delete + the test. Don't over-engineer — DJ-149's drift detection lives in the playbook, not in Go.

- [ ] **Step 3: Run the state package tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/state/... ./internal/reconcile/... -v 2>&1 | tail -10`
Expected: all pass after the migration.

- [ ] **Step 4: Commit**

```bash
git add internal/state/state.go internal/reconcile/classify.go
git commit -m "refactor(state): migrate SpecHash to SpecHashes map per DJ-149"
```

---

### Task 5: Add daemon-side `SpecHashes` computation helper

**Files:**
- Create: `internal/state/spec_hashes.go`
- Test: `internal/state/spec_hashes_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/state/spec_hashes_test.go`:

```go
package state

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubManifestGetter implements the minimal interface ComputeSpecHashes needs:
// given a spec id, return its body bytes (any deterministic encoding).
type stubGetter map[string][]byte

func (s stubGetter) BodyBytes(id string) ([]byte, bool) {
	b, ok := s[id]
	return b, ok
}

func TestComputeSpecHashes_IncludesAllCitations(t *testing.T) {
	approach := spec.Approach{
		ID:       "app-feat-auth",
		ParentID: "feat-auth",
		Decisions: []string{"dec-auth-approach", "dec-jwt-library"},
		Advances:  []string{"goal-secure-by-default"},
		Respects:  []string{"agoal-fundraising"},
	}
	getter := stubGetter{
		"app-feat-auth":          []byte("approach body"),
		"feat-auth":              []byte("feature body"),
		"dec-auth-approach":      []byte("decision body"),
		"dec-jwt-library":        []byte("another decision"),
		"goal-secure-by-default": []byte("goal body"),
		"agoal-fundraising":      []byte("anti-goal body"),
	}
	hashes, err := ComputeSpecHashes(approach, getter)
	require.NoError(t, err)
	// All 6 ids present
	assert.Contains(t, hashes, "app-feat-auth")
	assert.Contains(t, hashes, "feat-auth")
	assert.Contains(t, hashes, "dec-auth-approach")
	assert.Contains(t, hashes, "dec-jwt-library")
	assert.Contains(t, hashes, "goal-secure-by-default")
	assert.Contains(t, hashes, "agoal-fundraising")
	// Format is sha256:<hex>
	for id, h := range hashes {
		assert.True(t, len(h) > 7 && h[:7] == "sha256:", "id %s hash %s missing sha256: prefix", id, h)
	}
}

func TestComputeSpecHashes_OrderIndependent(t *testing.T) {
	a1 := spec.Approach{
		ID:        "app-x",
		ParentID:  "feat-x",
		Decisions: []string{"dec-a", "dec-b"},
	}
	a2 := spec.Approach{
		ID:        "app-x",
		ParentID:  "feat-x",
		Decisions: []string{"dec-b", "dec-a"}, // reordered
	}
	getter := stubGetter{
		"app-x":  []byte("body"),
		"feat-x": []byte("feat"),
		"dec-a":  []byte("a"),
		"dec-b":  []byte("b"),
	}
	h1, _ := ComputeSpecHashes(a1, getter)
	h2, _ := ComputeSpecHashes(a2, getter)
	assert.Equal(t, h1, h2, "decision reordering must not change the hash map")
}

func TestComputeSpecHashes_MissingIdReturnsError(t *testing.T) {
	approach := spec.Approach{ID: "app-x", ParentID: "feat-x"}
	getter := stubGetter{
		"app-x": []byte("body"),
		// feat-x missing
	}
	_, err := ComputeSpecHashes(approach, getter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "feat-x")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/state/ -run TestComputeSpecHashes -v`
Expected: FAIL — ComputeSpecHashes undefined.

- [ ] **Step 3: Implement the helper**

Create `internal/state/spec_hashes.go`:

```go
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
			continue // deduplicate (rare but possible if approach cites the same id in multiple slots)
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
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/state/ -run TestComputeSpecHashes -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/state/spec_hashes.go internal/state/spec_hashes_test.go
git commit -m "feat(state): ComputeSpecHashes one-hop upstream subgraph helper (DJ-149)"
```

---

## Phase 3 — Overlay extension

### Task 6: Extend `sessionOverlay` with `stateOverrides` + `stateDeleted`

**Files:**
- Modify: `internal/agent/spec_store_overlay.go`
- Test: append to `internal/agent/spec_store_overlay_test.go`

- [ ] **Step 1: Write failing tests for the new overlay methods**

Append to `internal/agent/spec_store_overlay_test.go`:

```go
// DJ-149 — state overrides on the sessionOverlay. Mirrors the spec
// entries/deleted shape: stateOverrides hold would-be ReconciliationState
// records; stateDeleted masks them on read.

import (
	"github.com/glorious-beard/locutus/internal/state"
)

func TestSessionOverlay_StatePutAndLookup(t *testing.T) {
	o := newSessionOverlay()
	rs := state.ReconciliationState{
		ApproachID: "app-feat-foo",
		Status:     state.StatusLive,
		SpecHashes: map[string]string{"app-feat-foo": "sha256:abc"},
		Artifacts:  map[string]string{"f.go": "sha256:def"},
	}
	o.putState("state_record_reconciliation", "app-feat-foo", rs)

	got, ok := o.lookupState("app-feat-foo")
	require.True(t, ok)
	require.NotNil(t, got)
	assert.Equal(t, "app-feat-foo", got.ApproachID)
	assert.Equal(t, state.StatusLive, got.Status)

	caps := o.capturedList()
	require.Len(t, caps, 1)
	assert.Equal(t, "state_record_reconciliation", caps[0].Tool)
	assert.Equal(t, "app-feat-foo", caps[0].ID)
}

func TestSessionOverlay_StateDeleteMasksLookup(t *testing.T) {
	o := newSessionOverlay()
	o.deleteState("state_delete_record", "app-feat-foo")

	_, ok := o.lookupState("app-feat-foo")
	assert.False(t, ok, "deleted state record must report missing")
	assert.True(t, o.isStateDeleted("app-feat-foo"))

	caps := o.capturedList()
	require.Len(t, caps, 1)
	assert.Equal(t, "state_delete_record", caps[0].Tool)
}

func TestSessionOverlay_StatePutAfterDeleteUnmasks(t *testing.T) {
	o := newSessionOverlay()
	o.deleteState("state_delete_record", "app-x")
	o.putState("state_record_reconciliation", "app-x", state.ReconciliationState{ApproachID: "app-x"})
	_, ok := o.lookupState("app-x")
	assert.True(t, ok, "putState after deleteState must unmask")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/agent/ -run TestSessionOverlay_State -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Add overlay fields + methods**

In `internal/agent/spec_store_overlay.go`, find the `sessionOverlay` struct. Add the new fields after the existing `manifestOverride *ManifestOverride`:

```go
// stateOverrides holds would-be ReconciliationState records keyed
// by approach id. Written by state_record_reconciliation +
// state_refresh_artifacts + state_mark_status capture closures
// under dry-run. Read by OverlayView.GetState. Per DJ-149 §10.
stateOverrides map[string]*state.ReconciliationState

// stateDeleted marks approach ids whose state records would be
// removed by state_delete_record under dry-run. Lookup masks the
// base FileStateStore the same way spec deleted entries mask the
// base SpecStore.
stateDeleted map[string]struct{}
```

Add `"github.com/glorious-beard/locutus/internal/state"` to the file's imports if not already present.

Add `stateOverrides`, `stateDeleted` initialization to `newSessionOverlay`:

```go
func newSessionOverlay() *sessionOverlay {
    return &sessionOverlay{
        entries:        make(map[storeKey]*StoreEntry),
        deleted:        make(map[storeKey]struct{}),
        stateOverrides: make(map[string]*state.ReconciliationState),
        stateDeleted:   make(map[string]struct{}),
    }
}
```

Add the four new methods at the bottom of the file:

```go
// putState captures a would-be state record write under dry-run.
// Mirrors put() for spec entries. The captured slice gains a
// CapturedMutation entry for spec_dry_run_report's surface.
func (o *sessionOverlay) putState(tool, approachID string, rs state.ReconciliationState) {
    o.mu.Lock()
    defer o.mu.Unlock()
    o.stateOverrides[approachID] = &rs
    delete(o.stateDeleted, approachID) // put after delete unmasks
    o.captured = append(o.captured, CapturedMutation{
        Tool:      tool,
        Kind:      KindApproach, // state records are per-approach; use the approach kind for consistency in the report
        ID:        approachID,
        Body:      rs,
        Timestamp: time.Now().UTC(),
    })
}

// deleteState marks an approach's state record as would-be deleted.
func (o *sessionOverlay) deleteState(tool, approachID string) {
    o.mu.Lock()
    defer o.mu.Unlock()
    o.stateDeleted[approachID] = struct{}{}
    delete(o.stateOverrides, approachID)
    o.captured = append(o.captured, CapturedMutation{
        Tool:      tool,
        Kind:      KindApproach,
        ID:        approachID,
        Timestamp: time.Now().UTC(),
    })
}

// lookupState returns (record, true) when the overlay holds a
// would-be state record for approachID; (nil, false) when it
// doesn't OR when stateDeleted masks the lookup. Read-only contract
// per the spec entries' lookup.
func (o *sessionOverlay) lookupState(approachID string) (*state.ReconciliationState, bool) {
    o.mu.RLock()
    defer o.mu.RUnlock()
    if _, gone := o.stateDeleted[approachID]; gone {
        return nil, false
    }
    rs, ok := o.stateOverrides[approachID]
    return rs, ok
}

// isStateDeleted reports whether this session has marked the
// approach's state record for deletion. Used by OverlayView read
// merging to mask the base FileStateStore.
func (o *sessionOverlay) isStateDeleted(approachID string) bool {
    o.mu.RLock()
    defer o.mu.RUnlock()
    _, gone := o.stateDeleted[approachID]
    return gone
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/agent/ -run TestSessionOverlay_State -race -v`
Expected: PASS (3 tests under race).

- [ ] **Step 5: Run full agent suite**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/agent/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/spec_store_overlay.go internal/agent/spec_store_overlay_test.go
git commit -m "feat(agent): sessionOverlay state overrides + deletions (DJ-149)"
```

---

### Task 7: Extend `SpecStore` and `OverlayView` with state entry points

**Files:**
- Modify: `internal/agent/spec_store.go` (add Overlay* state methods, accept FileStateStore in constructor)
- Modify: `internal/agent/spec_store_overlay.go` (add OverlayView state methods)
- Test: append to `internal/agent/spec_store_overlay_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/agent/spec_store_overlay_test.go`:

```go
func TestSpecStore_OverlayStatePutAndView(t *testing.T) {
    store, err := NewSpecStore(specio.NewMemFS())
    require.NoError(t, err)

    type fakeSess struct{ id string }
    sess := &fakeSess{id: "s1"}
    store.RegisterOverlay(sess)
    t.Cleanup(func() { store.UnregisterOverlay(sess) })

    rs := state.ReconciliationState{
        ApproachID: "app-feat-foo",
        Status:     state.StatusLive,
    }
    err = store.OverlayPutState(sess, "state_record_reconciliation", "app-feat-foo", rs)
    require.NoError(t, err)

    view := store.OverlayView(sess)
    got, ok := view.GetState("app-feat-foo")
    require.True(t, ok)
    assert.Equal(t, state.StatusLive, got.Status)

    // Caps include the state capture
    caps := store.OverlayCaptured(sess)
    require.Len(t, caps, 1)
    assert.Equal(t, "state_record_reconciliation", caps[0].Tool)
}

func TestSpecStore_OverlayStateDeleteMasks(t *testing.T) {
    store, err := NewSpecStore(specio.NewMemFS())
    require.NoError(t, err)

    type fakeSess struct{ id string }
    sess := &fakeSess{id: "s1"}
    store.RegisterOverlay(sess)
    t.Cleanup(func() { store.UnregisterOverlay(sess) })

    err = store.OverlayDeleteState(sess, "state_delete_record", "app-feat-foo")
    require.NoError(t, err)

    view := store.OverlayView(sess)
    _, ok := view.GetState("app-feat-foo")
    assert.False(t, ok, "deleted record masked from view")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/agent/ -run TestSpecStore_OverlayState -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Add SpecStore state methods**

In `internal/agent/spec_store.go`, near the existing `OverlayPut` / `OverlayDelete` (around line 1180+), add:

```go
// OverlayPutState applies a would-be ReconciliationState write to
// the session's overlay. Returns an error if the session has no
// registered overlay (caller bug — the captureOnly wrapper guards
// this in production). Per DJ-149 §10.
func (s *SpecStore) OverlayPutState(sess any, tool, approachID string, rs state.ReconciliationState) error {
    o := s.overlayFor(sess)
    if o == nil {
        return fmt.Errorf("OverlayPutState: session has no registered overlay (call RegisterOverlay first)")
    }
    o.putState(tool, approachID, rs)
    return nil
}

// OverlayDeleteState marks a would-be state record deletion on the
// session's overlay.
func (s *SpecStore) OverlayDeleteState(sess any, tool, approachID string) error {
    o := s.overlayFor(sess)
    if o == nil {
        return fmt.Errorf("OverlayDeleteState: session has no registered overlay")
    }
    o.deleteState(tool, approachID)
    return nil
}
```

Add `"github.com/glorious-beard/locutus/internal/state"` to the file's imports if not already present.

- [ ] **Step 4: Add OverlayView state methods**

In `internal/agent/spec_store_overlay.go`, near the existing `OverlayView.Lookup`, add:

```go
// GetState returns the session's overlay-merged view of an
// approach's ReconciliationState: overlay-held wins, deleted-mask
// returns missing, otherwise falls through to the base
// FileStateStore. Sessions without an overlay get base-only.
func (v *OverlayView) GetState(approachID string) (*state.ReconciliationState, bool) {
    if v.overlay != nil {
        if v.overlay.isStateDeleted(approachID) {
            return nil, false
        }
        if rs, ok := v.overlay.lookupState(approachID); ok {
            return rs, true
        }
    }
    // Fall through to base FileStateStore via the store's state accessor.
    return v.store.baseStateLookup(approachID)
}

// ListStateRecords returns all approach ids known to state, merging
// overlay additions and removals over the base FileStateStore.
// Order is base-then-overlay-additions, with overlay-deleted ids
// removed from the base set.
func (v *OverlayView) ListStateRecords() []string {
    if v.overlay == nil {
        return v.store.baseStateList()
    }
    seen := make(map[string]struct{})
    out := []string{}
    for _, id := range v.store.baseStateList() {
        if v.overlay.isStateDeleted(id) {
            continue
        }
        seen[id] = struct{}{}
        out = append(out, id)
    }
    v.overlay.mu.RLock()
    for id := range v.overlay.stateOverrides {
        if _, dup := seen[id]; dup {
            continue
        }
        out = append(out, id)
    }
    v.overlay.mu.RUnlock()
    return out
}
```

- [ ] **Step 5: Add SpecStore base-state accessors**

In `internal/agent/spec_store.go`, add a small adapter that the OverlayView can use to reach the FileStateStore:

```go
// baseStateLookup is the SpecStore's adapter into the daemon's
// FileStateStore. Set by NewSpecServer at daemon startup; nil in
// test contexts where state isn't wired (those get empty results).
type stateLookupFn func(approachID string) (*state.ReconciliationState, bool)
type stateListFn func() []string

// fields on SpecStore (add to struct):
//   baseStateLookupFn stateLookupFn
//   baseStateListFn   stateListFn

func (s *SpecStore) SetStateAccessors(lookup stateLookupFn, list stateListFn) {
    s.baseStateLookupFn = lookup
    s.baseStateListFn = list
}

func (s *SpecStore) baseStateLookup(approachID string) (*state.ReconciliationState, bool) {
    if s.baseStateLookupFn == nil {
        return nil, false
    }
    return s.baseStateLookupFn(approachID)
}

func (s *SpecStore) baseStateList() []string {
    if s.baseStateListFn == nil {
        return nil
    }
    return s.baseStateListFn()
}
```

- [ ] **Step 6: Run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/agent/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/spec_store.go internal/agent/spec_store_overlay.go internal/agent/spec_store_overlay_test.go
git commit -m "feat(agent): OverlayView GetState + ListStateRecords; SpecStore state accessors (DJ-149)"
```

---

## Phase 4 — State MCP tools

### Task 8: Add state tool input types + description constants

**Files:**
- Create: `internal/mcp/tools_state.go` (new file for state tool registrations + input types)

- [ ] **Step 1: Create the file with type definitions + description constants**

Create `internal/mcp/tools_state.go`:

```go
package mcp

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/glorious-beard/locutus/internal/agent"
    "github.com/glorious-beard/locutus/internal/state"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
    descStateRecordReconciliation = "Record a per-approach reconciliation outcome to .borg/state/<approach-id>.yaml. Called by the runtime after each adopt-phase implementation. Inputs: approach_id (app- prefix; must exist in the manifest), artifacts (map of relative file path to sha256:<hex> hash for every file in the phase's worktree diff), branch_name (the adopt/<NNN>-<approach-id> branch — for audit + operator review), test_outcome (\"passed\" or \"failed\" — honors DJ-068's test-asserted-live principle), test_command (what was actually run, e.g. \"go test ./...\"), test_output_excerpt (last N lines for context), message (optional free-form note). Server fills spec_hashes from the current spec graph (one-hop upstream subgraph) and stamps last_reconciled=now. Status derived: passed → live, failed → failed. Per DJ-149."

    descStateRefreshArtifacts = "Refresh the per-file artifact hashes on an existing state record without changing status. Used after the drift-classifier judges a code change trivial (formatting/imports/comments) — adopt accepts the change as the new baseline without regenerating. Inputs: approach_id, artifacts (the new hashes), reason (free-form, e.g. \"gofmt-style cleanup auto-accepted as trivial by drift-classifier\"). Server stamps last_reconciled=now. Status and spec_hashes unchanged. Per DJ-149."

    descStateMarkStatus = "Transition an approach's state record to an explicit status. Used by the runtime (e.g., marking in_progress before a long phase) or by the operator (e.g., manual planned/failed/out_of_spec). Inputs: approach_id, status (one of the 8 ReconcileStatus values: unplanned, planned, pre_flight, in_progress, live, failed, drifted, out_of_spec), message (optional free-form reason). Server stamps last_reconciled=now. spec_hashes and artifacts unchanged. Per DJ-149."

    descStateDeleteRecord = "Delete an approach's state record entirely. Used by the operator to retire an approach (parent superseded) or to forget previous reconciliation (start fresh on next adopt run). Inputs: approach_id, reason (required — matches spec_delete_goal's audit shape). Server removes the FileStateStore entry on commit. Per DJ-149."

    descStateListRecords = "Return a compact index of every state record: approach_id, status, last_reconciled, branch_name. Used by adopt's Step 1 to enumerate which approaches have been reconciled. No input. Overlay-aware: under dry-run, includes session-pending writes and excludes session-pending deletes. Per DJ-149."

    descStateGetRecord = "Batched body fetch for state records. Input: approach_ids (list of app- prefixed ids). Output: results map keyed by approach_id with full ReconciliationState (spec_hashes, artifacts, status, message, last_reconciled, branch_name); available_ids for ids that exist; missing for requested ids without records. Overlay-aware. Per DJ-149."
)

type stateRecordReconciliationInput struct {
    ApproachID        string            `json:"approach_id"`
    Artifacts         map[string]string `json:"artifacts"`
    BranchName        string            `json:"branch_name"`
    TestOutcome       string            `json:"test_outcome"` // "passed" | "failed"
    TestCommand       string            `json:"test_command"`
    TestOutputExcerpt string            `json:"test_output_excerpt,omitempty"`
    Message           string            `json:"message,omitempty"`
}

type stateRefreshArtifactsInput struct {
    ApproachID string            `json:"approach_id"`
    Artifacts  map[string]string `json:"artifacts"`
    Reason     string            `json:"reason"`
}

type stateMarkStatusInput struct {
    ApproachID string `json:"approach_id"`
    Status     string `json:"status"`
    Message    string `json:"message,omitempty"`
}

type stateDeleteRecordInput struct {
    ApproachID string `json:"approach_id"`
    Reason     string `json:"reason"`
}

type stateGetRecordInput struct {
    ApproachIDs []string `json:"approach_ids"`
}
```

- [ ] **Step 2: Verify the file compiles**

Run: `cd /Users/chetan/projects/locutus && go build ./internal/mcp/...`
Expected: clean (no registration yet; just types).

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/tools_state.go
git commit -m "feat(mcp): state tool input types + description constants (DJ-149)"
```

---

### Task 9: Register `state_record_reconciliation` + `state_refresh_artifacts`

**Files:**
- Modify: `internal/mcp/tools_state.go` (add registerStateTools function)
- Modify: `internal/mcp/server.go` (call registerStateTools from NewSpecServer; thread FileStateStore param)
- Test: `internal/mcp/tools_state_dry_run_test.go` (new)

- [ ] **Step 1: Update `NewSpecServer` to accept a FileStateStore**

In `internal/mcp/server.go`, find `func NewSpecServer`. Change its signature to add a `stateStore *state.FileStateStore` parameter (before `hist`):

```go
func NewSpecServer(store *agent.SpecStore, fsys specio.FS, reg *activity.Registry, stateStore *state.FileStateStore, hist *history.Historian) *mcp.Server {
```

In the body, after the existing setup, wire the state store into the SpecStore's accessors:

```go
if stateStore != nil {
    store.SetStateAccessors(
        func(approachID string) (*state.ReconciliationState, bool) {
            rs, err := stateStore.Load(approachID)
            if err != nil {
                return nil, false
            }
            return &rs, true
        },
        func() []string {
            recs, err := stateStore.Walk()
            if err != nil {
                return nil
            }
            out := make([]string, 0, len(recs))
            for _, rs := range recs {
                out = append(out, rs.ApproachID)
            }
            return out
        },
    )
}
```

Add `registerStateTools(server, store, stateStore)` after the existing `registerReadTools` / `registerWriteTools` calls (the call goes at the end of `NewSpecServer`).

Add the `"github.com/glorious-beard/locutus/internal/state"` import.

- [ ] **Step 2: Find every call site of `NewSpecServer` and update**

Run: `cd /Users/chetan/projects/locutus && grep -rn 'NewSpecServer(' --include='*.go'`

For each call site, add the `stateStore` argument. In production (`cmd/mcp.go` or similar), instantiate a real `state.NewFileStateStore(fsys, state.DefaultStateDir)`. In tests, pass `nil` (state tools then no-op for reads but capture cleanly under dry-run).

- [ ] **Step 3: Write the failing dry-run capture test**

Create `internal/mcp/tools_state_dry_run_test.go`:

```go
package mcp

import (
    "context"
    "testing"
    "time"

    "github.com/glorious-beard/locutus/internal/activity"
    "github.com/glorious-beard/locutus/internal/agent"
    "github.com/glorious-beard/locutus/internal/specio"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestDryRunCapturesStateWrites(t *testing.T) {
    cases := []struct {
        name string
        tool string
        args map[string]any
    }{
        {
            "record_reconciliation", "state_record_reconciliation",
            map[string]any{
                "approach_id":   "app-feat-foo",
                "artifacts":     map[string]any{"f.go": "sha256:abc"},
                "branch_name":   "adopt/001-app-feat-foo",
                "test_outcome":  "passed",
                "test_command":  "go test ./...",
            },
        },
        {
            "refresh_artifacts", "state_refresh_artifacts",
            map[string]any{
                "approach_id": "app-feat-foo",
                "artifacts":   map[string]any{"f.go": "sha256:new"},
                "reason":      "drift-classifier judged formatting-only",
            },
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            clearSessionRuntimes()
            fsys := specio.NewMemFS()
            store, err := agent.NewSpecStore(fsys)
            require.NoError(t, err)

            server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil, nil)
            serverT, clientT := mcp.NewInMemoryTransports()
            ss, err := server.Connect(context.Background(), serverT, nil)
            require.NoError(t, err)
            t.Cleanup(func() { _ = ss.Close() })

            client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
            cs, err := client.Connect(context.Background(), clientT, nil)
            require.NoError(t, err)
            t.Cleanup(func() { _ = cs.Close() })

            for d := time.Now().Add(time.Second); time.Now().Before(d); {
                if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
                    break
                }
                time.Sleep(5 * time.Millisecond)
            }
            storeSessionContext(ss, "claude-code", "headless", true, "markdown")
            store.RegisterOverlay(ss)
            t.Cleanup(func() { store.UnregisterOverlay(ss) })

            res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
            require.NoError(t, err)
            require.False(t, res.IsError, "tool result: %+v", res)

            caps := store.OverlayCaptured(ss)
            require.Len(t, caps, 1)
            assert.Equal(t, tc.tool, caps[0].Tool)
            assert.Equal(t, "app-feat-foo", caps[0].ID)
        })
    }
}
```

- [ ] **Step 4: Add the registrations**

In `internal/mcp/tools_state.go`, append:

```go
// captureStateRecordReconciliation lands a state_record_reconciliation
// call in the session's overlay under dry-run. Status is derived
// from test_outcome the same way the production handler derives it.
func captureStateRecordReconciliation(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateRecordReconciliationInput) (any, error) {
    return func(sess *mcp.ServerSession, in stateRecordReconciliationInput) (any, error) {
        rs, err := buildReconciliationStateRecord(in, time.Now().UTC())
        if err != nil {
            return nil, err
        }
        // SpecHashes left empty under dry-run — daemon would compute
        // them from the spec graph in the production path. Surfacing
        // them in the captured overlay would require fetching the
        // approach + cited bodies via the spec store at capture time;
        // the report's purpose is to surface the would-be mutation
        // shape, not to fully realize spec_hashes. Operators inspecting
        // dry-run output see "would write reconciliation; spec_hashes
        // computed at apply time."
        if err := store.OverlayPutState(sess, "state_record_reconciliation", in.ApproachID, rs); err != nil {
            return nil, err
        }
        return rs, nil
    }
}

func captureStateRefreshArtifacts(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateRefreshArtifactsInput) (any, error) {
    return func(sess *mcp.ServerSession, in stateRefreshArtifactsInput) (any, error) {
        // Under dry-run, capture a synthetic ReconciliationState that
        // marks the refresh intent — the real refresh path reads the
        // existing record + updates only Artifacts + LastReconciled.
        // Capture just records the would-be artifacts; downstream
        // overlay reads of the same record see the updated artifact map.
        existing, ok := captureReadStateForRefresh(store, sess, in.ApproachID)
        if !ok {
            return nil, fmt.Errorf("state_refresh_artifacts: no state record exists for %q under dry-run overlay nor base store", in.ApproachID)
        }
        existing.Artifacts = in.Artifacts
        existing.LastReconciled = time.Now().UTC()
        if err := store.OverlayPutState(sess, "state_refresh_artifacts", in.ApproachID, *existing); err != nil {
            return nil, err
        }
        return existing, nil
    }
}

// buildReconciliationStateRecord constructs the partial record the
// capture closure stores. The production handler (registered below)
// will additionally populate SpecHashes server-side from the current
// spec graph and override status from test_outcome.
func buildReconciliationStateRecord(in stateRecordReconciliationInput, syncedAt time.Time) (state.ReconciliationState, error) {
    if strings.TrimSpace(in.ApproachID) == "" {
        return state.ReconciliationState{}, fmt.Errorf("approach_id is required")
    }
    if !strings.HasPrefix(in.ApproachID, "app-") {
        return state.ReconciliationState{}, fmt.Errorf("approach_id %q must use the app- prefix", in.ApproachID)
    }
    if len(in.Artifacts) == 0 {
        return state.ReconciliationState{}, fmt.Errorf("artifacts is required (at least one path → hash)")
    }
    for path, hash := range in.Artifacts {
        if !strings.HasPrefix(hash, "sha256:") || len(hash) <= len("sha256:") {
            return state.ReconciliationState{}, fmt.Errorf("artifacts[%q] must be in sha256:<hex> form; got %q", path, hash)
        }
    }
    var status state.ReconcileStatus
    switch strings.TrimSpace(in.TestOutcome) {
    case "passed":
        status = state.StatusLive
    case "failed":
        status = state.StatusFailed
    default:
        return state.ReconciliationState{}, fmt.Errorf("test_outcome must be \"passed\" or \"failed\"; got %q", in.TestOutcome)
    }
    msg := in.Message
    if in.TestOutputExcerpt != "" {
        if msg != "" {
            msg += "\n\n"
        }
        msg += "Test command: " + in.TestCommand + "\nOutput excerpt:\n" + in.TestOutputExcerpt
    }
    return state.ReconciliationState{
        ApproachID:     in.ApproachID,
        Artifacts:      in.Artifacts,
        Status:         status,
        Message:        msg,
        LastReconciled: syncedAt,
        WorkstreamID:   in.BranchName, // repurpose for branch_name audit; field is operator-facing only
    }, nil
}

// captureReadStateForRefresh reads the would-be-current state for
// an approach: consult overlay first, then base store.
func captureReadStateForRefresh(store *agent.SpecStore, sess *mcp.ServerSession, approachID string) (*state.ReconciliationState, bool) {
    view := store.OverlayView(sess)
    if rs, ok := view.GetState(approachID); ok {
        return rs, true
    }
    return nil, false
}

func registerStateTools(server *mcp.Server, store *agent.SpecStore, stateStore *state.FileStateStore) {
    // state_record_reconciliation
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_record_reconciliation",
        Description: descStateRecordReconciliation,
    }, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateRecordReconciliationInput) (*mcp.CallToolResult, any, error) {
        rs, err := buildReconciliationStateRecord(in, time.Now().UTC())
        if err != nil {
            return errorResult(err.Error()), nil, nil
        }
        // Compute SpecHashes server-side from the current spec graph.
        approach, ok := loadApproachForStateRecord(store, in.ApproachID)
        if !ok {
            return errorResult(fmt.Sprintf("state_record_reconciliation: approach %q not found in manifest", in.ApproachID)), nil, nil
        }
        hashes, err := state.ComputeSpecHashes(approach, specStoreBodyGetter(store))
        if err != nil {
            return errorResult(fmt.Sprintf("state_record_reconciliation: %v", err)), nil, nil
        }
        rs.SpecHashes = hashes
        if stateStore == nil {
            return errorResult("state_record_reconciliation: daemon has no FileStateStore wired"), nil, nil
        }
        if err := stateStore.Save(rs); err != nil {
            return errorResult(fmt.Sprintf("state_record_reconciliation: %v", err)), nil, nil
        }
        return textResult(fmt.Sprintf("Recorded reconciliation for %s (status=%s, %d artifacts, branch=%s).", rs.ApproachID, rs.Status, len(rs.Artifacts), in.BranchName)), nil, nil
    }, captureStateRecordReconciliation(store)))

    // state_refresh_artifacts
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_refresh_artifacts",
        Description: descStateRefreshArtifacts,
    }, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateRefreshArtifactsInput) (*mcp.CallToolResult, any, error) {
        if stateStore == nil {
            return errorResult("state_refresh_artifacts: daemon has no FileStateStore wired"), nil, nil
        }
        existing, err := stateStore.Load(in.ApproachID)
        if err != nil {
            return errorResult(fmt.Sprintf("state_refresh_artifacts: no state record for %q", in.ApproachID)), nil, nil
        }
        existing.Artifacts = in.Artifacts
        existing.LastReconciled = time.Now().UTC()
        if in.Reason != "" {
            existing.Message = "Trivial drift accepted: " + in.Reason
        }
        if err := stateStore.Save(existing); err != nil {
            return errorResult(fmt.Sprintf("state_refresh_artifacts: %v", err)), nil, nil
        }
        return textResult(fmt.Sprintf("Refreshed artifacts for %s (%d files).", in.ApproachID, len(in.Artifacts))), nil, nil
    }, captureStateRefreshArtifacts(store)))
}

// loadApproachForStateRecord fetches an Approach body via the
// SpecStore's batched GetSpec for hash computation.
func loadApproachForStateRecord(store *agent.SpecStore, approachID string) (spec.Approach, bool) {
    res := store.GetSpec([]string{approachID})
    entry, ok := res.Results[approachID]
    if !ok || entry.Status == agent.SpecGetMissing {
        return spec.Approach{}, false
    }
    a, ok := entry.Body.(spec.Approach)
    return a, ok
}

// specStoreBodyGetter adapts the SpecStore's GetSpec to ComputeSpecHashes's BodyGetter interface.
type specStoreBodyGetter agent.SpecStore_BodyGetterAdapter // see Step 5

// Step 5 below introduces the adapter type properly.
```

Add the `"github.com/glorious-beard/locutus/internal/spec"` import.

- [ ] **Step 5: Add the SpecStore body-getter adapter**

In `internal/agent/spec_store.go`, add a small method/helper:

```go
// BodyBytes returns a deterministic byte encoding of a spec node's
// body. Used by state.ComputeSpecHashes for hashing. Implementation:
// YAML-marshal the body via the existing per-kind storage path.
func (s *SpecStore) BodyBytes(id string) ([]byte, bool) {
    res := s.GetSpec([]string{id})
    entry, ok := res.Results[id]
    if !ok || entry.Status == SpecGetMissing {
        return nil, false
    }
    // Use YAML-style determinism: marshal via yaml.v3.
    b, err := yaml.Marshal(entry.Body)
    if err != nil {
        return nil, false
    }
    return b, true
}
```

Add `"gopkg.in/yaml.v3"` to the imports.

Replace the `specStoreBodyGetter` line in `internal/mcp/tools_state.go` with a clean wrapper:

```go
func specStoreBodyGetter(s *agent.SpecStore) state.BodyGetter {
    return s
}
```

(This works because the `*agent.SpecStore` now has a `BodyBytes(id) ([]byte, bool)` method matching the `state.BodyGetter` interface.)

- [ ] **Step 6: Run the dry-run capture tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -run TestDryRunCapturesStateWrites -v`
Expected: PASS (2 subtests).

- [ ] **Step 7: Full mcp suite under race**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/tools_state.go internal/mcp/server.go internal/mcp/tools_state_dry_run_test.go internal/agent/spec_store.go cmd/mcp.go
git commit -m "feat(mcp): register state_record_reconciliation + state_refresh_artifacts (DJ-149)"
```

---

### Task 10: Register `state_mark_status` + `state_delete_record`

**Files:**
- Modify: `internal/mcp/tools_state.go` (add two registrations + capture closures)
- Test: append to `internal/mcp/tools_state_dry_run_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/mcp/tools_state_dry_run_test.go`:

```go
func TestDryRunCapturesStateMarkStatus(t *testing.T) {
    clearSessionRuntimes()
    fsys := specio.NewMemFS()
    store, err := agent.NewSpecStore(fsys)
    require.NoError(t, err)
    server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil, nil)
    serverT, clientT := mcp.NewInMemoryTransports()
    ss, _ := server.Connect(context.Background(), serverT, nil)
    t.Cleanup(func() { _ = ss.Close() })
    client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
    cs, _ := client.Connect(context.Background(), clientT, nil)
    t.Cleanup(func() { _ = cs.Close() })
    for d := time.Now().Add(time.Second); time.Now().Before(d); {
        if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
            break
        }
        time.Sleep(5 * time.Millisecond)
    }
    storeSessionContext(ss, "claude-code", "headless", true, "markdown")
    store.RegisterOverlay(ss)
    t.Cleanup(func() { store.UnregisterOverlay(ss) })

    res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
        Name: "state_mark_status",
        Arguments: map[string]any{
            "approach_id": "app-feat-foo",
            "status":      "planned",
            "message":     "operator manually scheduled for next adopt",
        },
    })
    require.NoError(t, err)
    require.False(t, res.IsError)

    caps := store.OverlayCaptured(ss)
    require.Len(t, caps, 1)
    assert.Equal(t, "state_mark_status", caps[0].Tool)
    assert.Equal(t, "app-feat-foo", caps[0].ID)
}

func TestDryRunCapturesStateDeleteRecord(t *testing.T) {
    clearSessionRuntimes()
    fsys := specio.NewMemFS()
    store, err := agent.NewSpecStore(fsys)
    require.NoError(t, err)
    server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil, nil)
    serverT, clientT := mcp.NewInMemoryTransports()
    ss, _ := server.Connect(context.Background(), serverT, nil)
    t.Cleanup(func() { _ = ss.Close() })
    client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
    cs, _ := client.Connect(context.Background(), clientT, nil)
    t.Cleanup(func() { _ = cs.Close() })
    for d := time.Now().Add(time.Second); time.Now().Before(d); {
        if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
            break
        }
        time.Sleep(5 * time.Millisecond)
    }
    storeSessionContext(ss, "claude-code", "headless", true, "markdown")
    store.RegisterOverlay(ss)
    t.Cleanup(func() { store.UnregisterOverlay(ss) })

    res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
        Name: "state_delete_record",
        Arguments: map[string]any{
            "approach_id": "app-feat-foo",
            "reason":      "parent feature retired",
        },
    })
    require.NoError(t, err)
    require.False(t, res.IsError)

    caps := store.OverlayCaptured(ss)
    require.Len(t, caps, 1)
    assert.Equal(t, "state_delete_record", caps[0].Tool)
}
```

- [ ] **Step 2: Add the registrations + capture closures**

In `internal/mcp/tools_state.go`, append to `registerStateTools`:

```go
    // state_mark_status
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_mark_status",
        Description: descStateMarkStatus,
    }, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateMarkStatusInput) (*mcp.CallToolResult, any, error) {
        if stateStore == nil {
            return errorResult("state_mark_status: daemon has no FileStateStore wired"), nil, nil
        }
        st, err := validateReconcileStatus(in.Status)
        if err != nil {
            return errorResult(err.Error()), nil, nil
        }
        existing, err := stateStore.Load(in.ApproachID)
        if err != nil {
            // Create a minimal record if none exists; operators marking
            // a planned status on a fresh approach is a valid path.
            existing = state.ReconciliationState{ApproachID: in.ApproachID}
        }
        existing.Status = st
        existing.LastReconciled = time.Now().UTC()
        if in.Message != "" {
            existing.Message = in.Message
        }
        if err := stateStore.Save(existing); err != nil {
            return errorResult(fmt.Sprintf("state_mark_status: %v", err)), nil, nil
        }
        return textResult(fmt.Sprintf("Marked %s as %s.", in.ApproachID, st)), nil, nil
    }, captureStateMarkStatus(store)))

    // state_delete_record
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_delete_record",
        Description: descStateDeleteRecord,
    }, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in stateDeleteRecordInput) (*mcp.CallToolResult, any, error) {
        if stateStore == nil {
            return errorResult("state_delete_record: daemon has no FileStateStore wired"), nil, nil
        }
        if strings.TrimSpace(in.Reason) == "" {
            return errorResult("state_delete_record: reason is required"), nil, nil
        }
        if err := stateStore.Delete(in.ApproachID); err != nil {
            return errorResult(fmt.Sprintf("state_delete_record: %v", err)), nil, nil
        }
        return textResult(fmt.Sprintf("Deleted state record for %s (reason: %s).", in.ApproachID, in.Reason)), nil, nil
    }, captureStateDeleteRecord(store)))
```

Also add the capture closures + validator helper near the top of the file (alongside the existing capture closures):

```go
func captureStateMarkStatus(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateMarkStatusInput) (any, error) {
    return func(sess *mcp.ServerSession, in stateMarkStatusInput) (any, error) {
        st, err := validateReconcileStatus(in.Status)
        if err != nil {
            return nil, err
        }
        existing, ok := captureReadStateForRefresh(store, sess, in.ApproachID)
        if !ok {
            existing = &state.ReconciliationState{ApproachID: in.ApproachID}
        }
        existing.Status = st
        existing.LastReconciled = time.Now().UTC()
        if in.Message != "" {
            existing.Message = in.Message
        }
        if err := store.OverlayPutState(sess, "state_mark_status", in.ApproachID, *existing); err != nil {
            return nil, err
        }
        return existing, nil
    }
}

func captureStateDeleteRecord(store *agent.SpecStore) func(sess *mcp.ServerSession, in stateDeleteRecordInput) (any, error) {
    return func(sess *mcp.ServerSession, in stateDeleteRecordInput) (any, error) {
        if strings.TrimSpace(in.Reason) == "" {
            return nil, fmt.Errorf("reason is required")
        }
        if err := store.OverlayDeleteState(sess, "state_delete_record", in.ApproachID); err != nil {
            return nil, err
        }
        return map[string]string{"approach_id": in.ApproachID, "reason": in.Reason}, nil
    }
}

func validateReconcileStatus(s string) (state.ReconcileStatus, error) {
    switch state.ReconcileStatus(strings.TrimSpace(s)) {
    case state.StatusUnplanned, state.StatusPlanned, state.StatusPreFlight,
        state.StatusInProgress, state.StatusLive, state.StatusFailed,
        state.StatusDrifted, state.StatusOutOfSpec:
        return state.ReconcileStatus(s), nil
    default:
        return "", fmt.Errorf("status %q is not one of the 8 valid ReconcileStatus values (unplanned/planned/pre_flight/in_progress/live/failed/drifted/out_of_spec)", s)
    }
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -run 'TestDryRunCapturesStateMarkStatus|TestDryRunCapturesStateDeleteRecord' -v`
Expected: PASS (2 tests).

- [ ] **Step 4: Full mcp suite under race**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_state.go internal/mcp/tools_state_dry_run_test.go
git commit -m "feat(mcp): register state_mark_status + state_delete_record (DJ-149)"
```

---

### Task 11: Register `state_list_records` + `state_get_record`

**Files:**
- Modify: `internal/mcp/tools_state.go` (add two read tool registrations)
- Test: append to `internal/mcp/tools_state_dry_run_test.go` (overlay-aware read tests)

- [ ] **Step 1: Append failing tests**

Append to `internal/mcp/tools_state_dry_run_test.go`:

```go
func TestDryRunStateReadAfterWrite(t *testing.T) {
    clearSessionRuntimes()
    fsys := specio.NewMemFS()
    store, err := agent.NewSpecStore(fsys)
    require.NoError(t, err)
    server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil, nil)
    serverT, clientT := mcp.NewInMemoryTransports()
    ss, _ := server.Connect(context.Background(), serverT, nil)
    t.Cleanup(func() { _ = ss.Close() })
    client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
    cs, _ := client.Connect(context.Background(), clientT, nil)
    t.Cleanup(func() { _ = cs.Close() })
    for d := time.Now().Add(time.Second); time.Now().Before(d); {
        if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
            break
        }
        time.Sleep(5 * time.Millisecond)
    }
    storeSessionContext(ss, "claude-code", "headless", true, "markdown")
    store.RegisterOverlay(ss)
    t.Cleanup(func() { store.UnregisterOverlay(ss) })

    // Write via the mark-status tool (cheaper than full reconciliation).
    _, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
        Name: "state_mark_status",
        Arguments: map[string]any{
            "approach_id": "app-feat-foo",
            "status":      "planned",
        },
    })
    require.NoError(t, err)

    // Read back via state_get_record — should see the overlay write.
    res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
        Name:      "state_get_record",
        Arguments: map[string]any{"approach_ids": []string{"app-feat-foo"}},
    })
    require.NoError(t, err)
    require.False(t, res.IsError)
    body := callToolResultText(t, res)
    assert.Contains(t, body, "app-feat-foo")
    assert.Contains(t, body, "planned")

    // List should include the overlay-only id.
    res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "state_list_records"})
    require.NoError(t, err)
    require.False(t, res.IsError)
    assert.Contains(t, callToolResultText(t, res), "app-feat-foo")
}
```

- [ ] **Step 2: Add the read tool registrations**

In `internal/mcp/tools_state.go`, append to `registerStateTools`:

```go
    // state_list_records (read-only; overlay-aware via OverlayView.ListStateRecords)
    type stateListRecordsOutput struct {
        Records []stateListEntry `json:"records"`
    }
    type stateListEntry struct {
        ApproachID     string `json:"approach_id"`
        Status         string `json:"status"`
        LastReconciled string `json:"last_reconciled,omitempty"`
        BranchName     string `json:"branch_name,omitempty"`
    }
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_list_records",
        Description: descStateListRecords,
    }, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, stateListRecordsOutput, error) {
        view := store.OverlayView(req.Session)
        ids := view.ListStateRecords()
        out := stateListRecordsOutput{Records: make([]stateListEntry, 0, len(ids))}
        for _, id := range ids {
            rs, ok := view.GetState(id)
            if !ok {
                continue
            }
            entry := stateListEntry{
                ApproachID: id,
                Status:     string(rs.Status),
                BranchName: rs.WorkstreamID, // repurposed for branch_name per Task 9
            }
            if !rs.LastReconciled.IsZero() {
                entry.LastReconciled = rs.LastReconciled.Format(time.RFC3339)
            }
            out.Records = append(out.Records, entry)
        }
        return nil, out, nil
    })

    // state_get_record (batched body fetch; overlay-aware)
    type stateGetRecordOutput struct {
        Results      map[string]state.ReconciliationState `json:"results"`
        AvailableIDs []string                              `json:"available_ids"`
        Missing      []string                              `json:"missing"`
    }
    mcp.AddTool(server, &mcp.Tool{
        Name:        "state_get_record",
        Description: descStateGetRecord,
    }, func(ctx context.Context, req *mcp.CallToolRequest, in stateGetRecordInput) (*mcp.CallToolResult, stateGetRecordOutput, error) {
        view := store.OverlayView(req.Session)
        out := stateGetRecordOutput{
            Results: make(map[string]state.ReconciliationState, len(in.ApproachIDs)),
        }
        for _, id := range in.ApproachIDs {
            rs, ok := view.GetState(id)
            if ok {
                out.Results[id] = *rs
                out.AvailableIDs = append(out.AvailableIDs, id)
            } else {
                out.Missing = append(out.Missing, id)
            }
        }
        return nil, out, nil
    })
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -run TestDryRunStateReadAfterWrite -v`
Expected: PASS.

- [ ] **Step 4: Full mcp suite under race**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -race -count=1 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_state.go internal/mcp/tools_state_dry_run_test.go
git commit -m "feat(mcp): register state_list_records + state_get_record (overlay-aware) (DJ-149)"
```

---

### Task 12: E2E persists-to-disk test for state_record_reconciliation

**Files:**
- Create: `internal/mcp/tools_state_test.go` (non-dry-run production path)

- [ ] **Step 1: Write the test**

Create `internal/mcp/tools_state_test.go`:

```go
package mcp

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/glorious-beard/locutus/internal/activity"
    "github.com/glorious-beard/locutus/internal/agent"
    "github.com/glorious-beard/locutus/internal/spec"
    "github.com/glorious-beard/locutus/internal/specio"
    "github.com/glorious-beard/locutus/internal/state"
    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestStateRecordReconciliation_PersistsToDisk(t *testing.T) {
    tmp := t.TempDir()
    fsys := specio.NewDiskFS(tmp)
    require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/spec/approaches"), 0o755))
    require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/spec/features"), 0o755))
    require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/state"), 0o755))

    store, err := agent.NewSpecStore(fsys)
    require.NoError(t, err)

    // Seed the approach + parent so ComputeSpecHashes can find bodies.
    require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))
    require.NoError(t, store.Put(agent.KindApproach, "app-feat-foo", spec.Approach{
        ID: "app-feat-foo", Title: "Approach foo", ParentID: "feat-foo", Body: "x",
    }, agent.OriginSettled))

    stateStore := state.NewFileStateStore(fsys, state.DefaultStateDir)

    server := NewSpecServer(store, fsys, activity.DefaultRegistry(), stateStore, nil)
    serverT, clientT := mcp.NewInMemoryTransports()
    ss, _ := server.Connect(context.Background(), serverT, nil)
    t.Cleanup(func() { _ = ss.Close() })
    client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
    cs, _ := client.Connect(context.Background(), clientT, nil)
    t.Cleanup(func() { _ = cs.Close() })

    res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
        Name: "state_record_reconciliation",
        Arguments: map[string]any{
            "approach_id":  "app-feat-foo",
            "artifacts":    map[string]any{"internal/foo/foo.go": "sha256:deadbeef"},
            "branch_name":  "adopt/001-app-feat-foo",
            "test_outcome": "passed",
            "test_command": "go test ./...",
        },
    })
    require.NoError(t, err)
    require.False(t, res.IsError, "result: %+v", res)

    // File persisted under .borg/state/app-feat-foo.yaml
    data, err := os.ReadFile(filepath.Join(tmp, ".borg/state/app-feat-foo.yaml"))
    require.NoError(t, err)
    s := string(data)
    assert.Contains(t, s, "approach_id: app-feat-foo")
    assert.Contains(t, s, "status: live")
    assert.Contains(t, s, "spec_hashes:")
    assert.Contains(t, s, "feat-foo: sha256:") // server-computed
    assert.Contains(t, s, "artifacts:")
    assert.Contains(t, s, "internal/foo/foo.go: sha256:deadbeef")
}
```

- [ ] **Step 2: Run + commit**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/mcp/ -run TestStateRecordReconciliation_PersistsToDisk -v`
Expected: PASS.

```bash
git add internal/mcp/tools_state_test.go
git commit -m "test(mcp): state_record_reconciliation e2e persists-to-disk (DJ-149)"
```

---

## Phase 5 — Subagent rewrites

### Task 13: Rewrite `internal/scaffold/agents/approach-regenerator.md`

**Files:**
- Modify: `internal/scaffold/agents/approach-regenerator.md`

> **MANDATORY**: Walk `docs/agent-conventions.md` checklist before editing per the user's strict requirement.

- [ ] **Step 1: Read the existing prompt**

Run: `cd /Users/chetan/projects/locutus && cat internal/scaffold/agents/approach-regenerator.md`

Note: existing prompt has `output_schema: RegenerateApproachResult` and references the council-era pipeline. Carries DJ-148 conventions drift.

- [ ] **Step 2: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: approach-regenerator
thinking: on
role: synthesis
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
max_iterations: 3
---
```

Rewrite the body to:
- Drop council-era references (`refine --supersede`, `ArtifactPaths`, `RegenerateApproachResult` shape)
- Update ID conventions: `app-<parent-id>` per DJ-087, `dec-<axis>` per DJ-133
- Reference the new state surface: parent or cited decision being revised invalidates the approach via either spec_mark_approach_drifted (explicit) OR SpecHashes mismatch on next adopt run (implicit per DJ-149)
- Specify the regeneration's output: a new approach body fed to spec_revise_approach; the runtime then re-implements in a fresh worktree on the next adopt run

The detailed prose preserves the existing analytical guidance about reconciling a stale approach against its new parent context. Just align terminology and references for current conventions.

- [ ] **Step 3: Verify hyphenated-ids invariant**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/agents/approach-regenerator.md
git commit -m "refactor(agents): rewrite approach-regenerator for DJ-149 state surface"
```

---

### Task 14: Author `internal/scaffold/agents/drift-classifier.md`

**Files:**
- Create: `internal/scaffold/agents/drift-classifier.md`

- [ ] **Step 1: Create the new agent prompt**

Create `internal/scaffold/agents/drift-classifier.md`:

```markdown
---
id: drift-classifier
thinking: off
role: drift-classification
models:
  - {provider: anthropic, tier: fast}
  - {provider: googleai, tier: fast}
  - {provider: openai, tier: fast}
---

# Identity

You are the drift classifier for Locutus adopt (DJ-149). Given a per-file diff between a stored artifact hash and the current file content, you judge whether the change is **trivial** (formatting, import order, whitespace, comment-only changes that don't alter program semantics) or **semantic** (any change that affects what the code does — added/removed/modified statements, expression changes, behavior changes).

You use a fast-tier model because the judgment is pattern recognition, not deliberation.

# Context

You receive as a user message:
- **path**: the relative file path
- **language**: language hint (go, typescript, python, etc.) inferred from the file extension
- **diff**: the unified diff between the stored content and current content

# Task

Classify the diff as `trivial` or `semantic`. Return exactly one of:

```
classification: trivial
reason: <one-line explanation, e.g., "gofmt-only changes" or "import reorder only" or "comment-text changes">
```

or

```
classification: semantic
reason: <one-line explanation, e.g., "added new function" or "changed conditional logic" or "modified return value">
```

# Rules

- **Default to semantic when uncertain.** False negatives (calling semantic change trivial) silently lose code; false positives (calling trivial change semantic) trigger an unnecessary regeneration. Conservatism wins.
- **Whitespace + formatting + blank lines = trivial.** Tools like gofmt, prettier, black do this routinely.
- **Import order changes = trivial.** Tools like goimports, prettier-plugin-organize-imports do this.
- **Comment text changes = trivial.** Doc updates, license headers, TODO additions/removals.
- **Type annotation additions = semantic.** They may relax/tighten the compiler's checks.
- **Identifier renames = semantic.** Even pure renames affect call sites elsewhere.
- **Logging additions = semantic.** Side effects matter.

# Anti-hallucination

If the diff is malformed or you can't parse it, return `classification: semantic` with `reason: diff unparseable; defaulting to semantic for safety`.

Do not output anything beyond the two-line classification + reason block.
```

- [ ] **Step 2: Verify invariants**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/agents/drift-classifier.md
git commit -m "feat(agents): drift-classifier agent for semantic-vs-trivial code drift (DJ-149)"
```

---

## Phase 6 — Playbook authoring

### Task 15: Author `internal/scaffold/plans/code_adoption.md` (default fallback)

**Files:**
- Modify: `internal/scaffold/plans/code_adoption.md` (currently a 19-line placeholder; rewrite entirely)

- [ ] **Step 1: Replace the placeholder content**

Replace `internal/scaffold/plans/code_adoption.md` entirely with a full playbook following Section 3 of DJ-149. The playbook covers Step 0 (preconditions) → Step 1 (read manifest + state) → Step 2 (identify worklist with drift detection) → Step 3 (write plan files to `.locutus/sessions/<sid>/plans/`) → Step 4 (dispatch runtime for implementation) → Step 5 (persist state) → Step 6 (drift classification report) → Step 7 (convergence verdict).

Reference [DJ-149's Section 2 prose](../../docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md) for the exact step semantics — every Step 0-7 from the DJ doc becomes a section in the playbook with the dispatch prose to the runtime.

Key playbook sections (in order):
- `# Code adoption playbook (one iteration)` — header
- `## Plan first` — TodoWrite preamble
- `## Start here` — call spec_list_manifest + state_list_records
- `## What you have` — MCP tools + state tools + Bash/Read/Task tools + subagents (drift-classifier, approach-regenerator)
- `## Invariants` — MCP-only spec/state mutations; GOALS.md read-only; LOCUTUS_DRY_RUN guards around worktree creation + code generation
- `## Step 0 — Precondition check` — GOALS.md + goal layer + non-empty work pool
- `## Step 1 — Read manifest + state` — spec_list_manifest + state_list_records + batched gets
- `## Step 2 — Identify worklist + drift classification` — per Section 2 of DJ-149 (set diff + hash diff; drift-classifier dispatch for code mismatches; status mapping)
- `## Step 3 — Write plan files` — per Section 3 of DJ-149 (frontmatter shape; one file per approach in worklist categories synthesize_and_implement/implement/regenerate)
- `## Step 4 — Dispatch runtime for implementation` — per Section 3 of DJ-149's dispatch instruction prose verbatim
- `## Step 5 — Persist state` — confirm state_record_reconciliation calls landed for every attempted phase
- `## Step 6 — Drift classification report` — per Section 5 of DJ-149
- `## Step 7 — Convergence verdict` — per Section 5 of DJ-149's verdict semantics

Reuse the wording from DJ-149's design doc directly where the design doc already has playbook-quality prose. Keep the playbook focused (not overly verbose); ~150-200 lines is the target.

- [ ] **Step 2: Verify the file parses**

Run: `cd /Users/chetan/projects/locutus && head -20 internal/scaffold/plans/code_adoption.md`
Expected: heading + readable structure.

- [ ] **Step 3: Run playbook tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/... 2>&1 | tail -10`
Expected: all pass (the invariants_dj144_test.go may need extension in Task 19; for now the existing tests pass).

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/plans/code_adoption.md
git commit -m "feat(plans): author code_adoption default playbook (DJ-149)"
```

---

### Task 16: Author `internal/scaffold/plans/code_adoption.claude-code.md` (provider overlay)

**Files:**
- Modify: `internal/scaffold/plans/code_adoption.claude-code.md` (rewrite from placeholder)

- [ ] **Step 1: Replace the existing placeholder with the dynamic-workflow overlay**

Mirror the structure of `internal/scaffold/plans/code_assimilation.claude-code.md` (from DJ-148), adapted for adopt. Key sections:
- `# Code Adoption (Claude Code — dynamic workflow)` — header with workflow framing
- `## Plan first` — TodoWrite plan
- `## Start here` — manifest + state reads
- `## Invariants` — same as default
- `## Convergence loop (up to {{max_iterations}} iterations)` — per-iteration: Step 1-6 from default; workflow drives parallel phase fan-out within Step 4 using `pipeline()` or `parallel()` primitives
- `## Closing report` — iterations run + per-approach results + branch list + test outcomes

The {{max_iterations}} token is injected from the activity registry (Task 17 sets it to 10).

- [ ] **Step 2: Verify + run tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/... 2>&1 | tail -5`
Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/plans/code_adoption.claude-code.md
git commit -m "feat(plans): code_adoption Claude Code dynamic-workflow overlay (DJ-149)"
```

---

### Task 17: Author `internal/scaffold/plans/code_adoption.interactive.md` (mode overlay)

**Files:**
- Modify: `internal/scaffold/plans/code_adoption.interactive.md` (currently a placeholder; rewrite)

- [ ] **Step 1: Author the interactive overlay**

Mirror `internal/scaffold/plans/code_assimilation.interactive.md` from DJ-148, adapted for adopt:

```markdown
# Code Adoption (interactive — Codex / Gemini)

This overlay applies to interactive Codex and Gemini sessions for the `code_adoption` activity. It wraps the default single-iteration playbook with the loop-state directive header so the agent self-drives iteration via the DJ-142 `spec_loop_*` MCP tools.

## Loop control

You are running this activity in **interactive mode**. The harness does NOT re-dispatch you between iterations — you drive iteration yourself via the daemon's loop-state tools:

- `mcp__locutus__spec_loop_begin` — call once at the start with `{activity: "code_adoption", target: ""}`. Returns the iteration cap and the current iteration number (starts at 1).
- `mcp__locutus__spec_loop_status` — call to check whether you should continue. Returns `should_continue: bool` (false when at cap or marked converged).
- `mcp__locutus__spec_advance_iteration` — call between iterations to increment the counter and record a per-iteration note for the report.

Do NOT emit a `converged:` verdict line for an outer harness — there is no harness in interactive mode; you are the harness. The loop terminates when (a) you've emitted `spec_loop_status` and it returned `should_continue: false`, or (b) you've decided no further work remains.

The iteration cap is set by the activity registry per DJ-138; this activity's default is `max_iterations: 10`.

---

(Below this header, the default `code_adoption.md` playbook body applies. Read it now and follow it for each iteration, calling `spec_advance_iteration` between iterations.)
```

- [ ] **Step 2: Verify + commit**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/... 2>&1 | tail -5`
Expected: all pass.

```bash
git add internal/scaffold/plans/code_adoption.interactive.md
git commit -m "feat(plans): code_adoption interactive overlay (DJ-149)"
```

---

## Phase 7 — Assimilate re-route + registry + cmd + CLI guard + docs

### Task 18: Re-route assimilate playbook to use state MCP tools

**Files:**
- Modify: `internal/scaffold/plans/code_assimilation.md` (Step 6 update — replace `spec_propose_approach`/`spec_revise_approach` hash-computation block with `state_record_reconciliation` flow)
- Modify: `internal/scaffold/plans/code_assimilation.claude-code.md` (same edit in the workflow overlay)

- [ ] **Step 1: Read the existing Step 6**

Run: `cd /Users/chetan/projects/locutus && grep -nA 20 'Step 6' internal/scaffold/plans/code_assimilation.md`

Note the existing flow: gap-analyst output → for each confirmed/revised/proposed feature/strategy, synthesize approach with source_hash, call spec_propose_approach with source_files+source_hash.

- [ ] **Step 2: Rewrite Step 6 for the new state flow**

In `internal/scaffold/plans/code_assimilation.md`, replace Step 6's "approach synthesis" block. New flow:
1. For each confirmed/revised/proposed feature/strategy, synthesize the approach body (no source_files/source_hash on the body — those went away with DJ-149's revert)
2. Call `spec_propose_approach` with id `app-<parent-id>` per DJ-087, parent_id, body, decisions, advances, respects (just the body fields; no source bindings here)
3. THEN compute the per-file artifact hashes via `shasum -a 256` over the bound files (same `find ... | sort | xargs shasum -a 256 ...` pipeline)
4. Call `state_record_reconciliation` with approach_id, artifacts (the computed map), branch_name (empty or "assimilate-derived"), test_outcome ("passed" if tests can be confirmed or operator-driven judgment; if no test path, follow the playbook's "halt for operator-actionable" branch), test_command, test_output_excerpt

The body + state are now committed via separate tools — body via spec_propose_approach (DJ-148 still), state via state_record_reconciliation (DJ-149 new).

- [ ] **Step 3: Apply the same edit to the Claude Code overlay**

Same edit in `internal/scaffold/plans/code_assimilation.claude-code.md` Step 6 (the workflow's per-phase block).

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/plans/code_assimilation.md internal/scaffold/plans/code_assimilation.claude-code.md
git commit -m "refactor(plans): re-route assimilate Step 6 to use state MCP tools per DJ-149"
```

---

### Task 19: Set `code_adoption.max_iterations: 10` + cmd doc refresh + invariants_dj144_test extension + CLI lock

**Files:**
- Modify: `internal/activity/agents-default.yaml`
- Modify: `cmd/adopt.go`
- Modify: `internal/scaffold/plans/invariants_dj144_test.go` (add the three adopt playbook files to the test cases)
- Modify: `cmd/activity_verb.go` (add `.locutus/adopt.lock` sidecar guard)

- [ ] **Step 1: Update activity registry**

In `internal/activity/agents-default.yaml`, find `code_adoption:` entry. Change `max_iterations` from 20 to 10:

```yaml
  code_adoption:
    runtimes:
      - claude-code
      - codex
      - gemini
    max_iterations: 10
```

- [ ] **Step 2: Refresh cmd/adopt.go doc**

In `cmd/adopt.go`, replace the existing doc-block on `AdoptCmd` with:

```go
// AdoptCmd dispatches the code_adoption activity (DJ-149).
// The activity reads the spec graph and the state store, identifies
// which approaches need work (unbound / spec-drifted / code-drifted /
// orphan-parent), dispatches the coding-agent runtime to implement
// in stacked worktrees (adopt/<NNN>-<approach-id> branches with
// phase-N+1-branches-off-N), and records reconciliation outcomes —
// including test-asserted live/failed status per DJ-068's honest-state
// principle — in .borg/state/.
//
// The runtime decides parallelism, branch ordering, and worktree
// management (per DJ-144's trajectory of trusting the runtime).
// Locutus writes plan files to .locutus/sessions/<sid>/plans/; the
// runtime reads and implements.
//
// Preconditions checked by the playbook in Step 0 (refused with a
// helpful error when missing): GOALS.md exists; goal layer is
// populated (operator ran `locutus refine goals` first); at least
// one approach OR one feat/strat without approach (otherwise nothing
// to adopt).
//
// Flags inherit from DJ-147: --dry-run + --format markdown|json;
// mutations capture via captureOnly at the MCP boundary; the
// playbook adds LOCUTUS_DRY_RUN guards around worktree creation +
// code generation.
type AdoptCmd struct {
    Scope  string `help:"Optional scope filter (approach id, parent feat-/strat- id, or directory prefix). Default: all approaches needing work."`
    DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
    Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}
```

- [ ] **Step 3: Add adopt-lock sidecar guard**

In `cmd/activity_verb.go` (the dispatcher used by adopt), add a lock-file check before dispatching `code_adoption`:

```go
// adoptLockPath returns the per-project adopt lock file location.
func adoptLockPath() string {
    return filepath.Join(".locutus", "adopt.lock")
}

// acquireAdoptLock atomically creates a lock file if absent, returning
// (false, existing_pid) if another adopt session is in flight.
// Caller MUST call releaseAdoptLock on exit (including panic recovery).
func acquireAdoptLock() (bool, string) {
    p := adoptLockPath()
    if data, err := os.ReadFile(p); err == nil {
        return false, strings.TrimSpace(string(data))
    }
    if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
        return false, ""
    }
    pid := fmt.Sprintf("%d", os.Getpid())
    if err := os.WriteFile(p, []byte(pid), 0o644); err != nil {
        return false, ""
    }
    return true, pid
}

func releaseAdoptLock() {
    _ = os.Remove(adoptLockPath())
}
```

In `runActivityVerb`, when activity is `"code_adoption"`, call `acquireAdoptLock()` before dispatch and `defer releaseAdoptLock()`. If acquisition fails, return an error:

```go
if activityName == "code_adoption" {
    ok, otherPID := acquireAdoptLock()
    if !ok {
        return fmt.Errorf("another adopt run is in flight (pid %s); wait for it to finish or remove %s if stale", otherPID, adoptLockPath())
    }
    defer releaseAdoptLock()
}
```

- [ ] **Step 4: Extend invariants_dj144_test.go**

In `internal/scaffold/plans/invariants_dj144_test.go`, find the `cases := []struct{...}` block listing spec-mutating playbooks. Add the three adopt playbook files to the test cases so they're also checked for: MCP-only spec mutations, GOALS.md read-only, LOCUTUS_DRY_RUN guards.

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -run TestInvariants -v`
Expected: PASS (with new adopt files in the cases).

- [ ] **Step 5: Verify build + full test**

Run: `cd /Users/chetan/projects/locutus && go build ./... && go vet ./... && go test ./... -race -count=1 2>&1 | grep -vE '^ok|no test files' | tail -10`
Expected: empty output (all pass).

- [ ] **Step 6: Commit**

```bash
git add internal/activity/agents-default.yaml cmd/adopt.go cmd/activity_verb.go internal/scaffold/plans/invariants_dj144_test.go
git commit -m "feat(cmd, activity, plans): adopt max_iterations + doc + CLI lock + invariants extension (DJ-149)"
```

---

### Task 20: Documentation (runtime-affordances + CLAUDE.md + council.md + DJ-149 future-work alignment)

**Files:**
- Modify: `docs/runtime-affordances.md`
- Modify: `CLAUDE.md`
- Modify: `docs/council.md`

- [ ] **Step 1: Add docs/runtime-affordances.md Adopt section**

In `docs/runtime-affordances.md`, after the existing Assimilate (DJ-148) section, add:

```markdown
## Adopt (DJ-149)

`locutus adopt` reads the spec graph and state store, identifies approaches needing work (unbound / spec-drifted / code-drifted / orphan-parent), and dispatches the coding-agent runtime to implement them in stacked worktrees. Each phase: `git worktree add ../<project>-adopt-<NNN>-<approach-id>` off the previous phase's branch (or operator base if phase 1); runtime implements using its native skills; runs the project's test suite (per DJ-068's test-asserted-live principle); calls `state_record_reconciliation` with the diff + test outcome.

The runtime decides parallelism + branch ordering. Branch naming: `adopt/<NNN>-<approach-id>` zero-padded for serial chains; `<NNN><letter>-<approach-id>` for parallel siblings at the same ordinal. Halt-on-first-failure; failed branch retained for operator inspection.

Preconditions (refused with helpful error): `GOALS.md` exists; goal layer populated; at least one approach OR one feat/strat without approach.

State surface from [DJ-068](decisions/dj-068-manifest-state-separation-kubernetes-inspired.md) + [DJ-096](decisions/dj-096-state-store-lives-under.md): per-approach record at `.borg/state/<approach-id>.yaml` with `SpecHashes` (one-hop upstream subgraph; catches cascade + refine drift per DJ-149's set+hash diff), `Artifacts` (per-file hashes for code drift), 8-status `ReconcileStatus`. All state mutations route through six new MCP tools (`state_record_reconciliation`, `state_refresh_artifacts`, `state_mark_status`, `state_delete_record`, `state_list_records`, `state_get_record`); writes are captureOnly-wrapped per DJ-147.

DJ-147 dry-run inherits automatically — state captures land in the overlay; the playbook adds `LOCUTUS_DRY_RUN` guards around worktree creation + code generation.
```

- [ ] **Step 2: Add CLAUDE.md bullet in Sources of Truth**

In `CLAUDE.md`, find the DJ-148 bullet. Add immediately after:

```markdown
- **`adopt` closes the spec → code gap (DJ-149).** Reads spec + state, identifies approaches needing work (unbound / spec-drifted / code-drifted / orphan-parent), dispatches the runtime to implement in stacked worktrees (`adopt/<NNN>-<approach-id>` with phase-N+1-branches-off-N), records state-asserted live status per DJ-068. Partially supersedes DJ-148's state-side fields on `spec.Approach`; state re-established at `.borg/state/` per DJ-068/DJ-096. `ReconciliationState.SpecHash string` migrates to `SpecHashes map[string]string` keyed by spec id for granular one-hop upstream subgraph drift detection (catches DJ-138 cascade drift, refine revisions to approach itself, renames as coincident add+remove). Six new MCP tools: write (`state_record_reconciliation`, `state_refresh_artifacts`, `state_mark_status`, `state_delete_record`) + read (`state_list_records`, `state_get_record`); all captureOnly-wrapped per DJ-147. Adds `drift-classifier` subagent (semantic-vs-trivial code drift judgment). Per-runtime convergence drivers from DJ-144. See [DJ-149](docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md).
```

- [ ] **Step 3: Update docs/council.md**

In `docs/council.md`, find where activity playbooks are listed. Add a paragraph naming adopt's subagent flow:

```markdown
**Adopt (DJ-149)** — spec → code reconciliation. Subagent flow: drift-classifier judges per-file code drifts (semantic vs trivial; trivial accepted via state_refresh_artifacts); approach-regenerator regenerates an approach body when its parent feature/strategy or cited decisions change (output fed to spec_revise_approach; runtime then re-implements in fresh worktree on next adopt run). The orchestrator computes the worklist from state, writes plan files to `.locutus/sessions/<sid>/plans/`, dispatches the runtime for stacked-worktree implementation, persists reconciliation via state MCP tools, surfaces drift classification in the report.
```

- [ ] **Step 4: Verify bijection + commit**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/docs/`
Expected: PASS.

```bash
git add docs/runtime-affordances.md CLAUDE.md docs/council.md
git commit -m "docs(dj-149): document adopt spec → code reconciliation"
```

---

## Phase 8 — Validation

### Task 21: Full suite + winplan handoff

- [ ] **Step 1: Full suite + vet + race**

Run: `cd /Users/chetan/projects/locutus && go build ./... && go vet ./... && go test ./... -race -count=1 2>&1 | grep -vE '^ok|no test files' | tail -15`
Expected: empty output.

- [ ] **Step 2: Rebuild binary**

```bash
cd /Users/chetan/projects/locutus
go build -o locutus .
ls -la locutus
```

- [ ] **Step 3: Stop winplan daemon**

```bash
( cd /Users/chetan/projects/winplan && ./locutus mcp-stop 2>&1 || echo "no daemon to stop" )
```

- [ ] **Step 4: Hand off winplan e2e to operator**

E2E validation requires a real Claude Code subprocess (operator's subscription). Surface as operator tasks:

```bash
# 1. Dry-run an adopt against winplan (precondition will fail because winplan has no source code)
( cd /Users/chetan/projects/winplan && ./locutus adopt --dry-run )

# 2. Dry-run with JSON output for jq inspection
( cd /Users/chetan/projects/winplan && ./locutus adopt --dry-run --format json | jq '.captured[]' )

# 3. On a real codebase with GOALS.md + spec + approaches + tests, dry-run
( cd <some-real-project> && ./locutus adopt --dry-run )

# 4. On a real codebase, full run (operator-attended; will create worktrees + generate code)
( cd <some-real-project> && ./locutus adopt )
```

---

## Self-Review

- **Spec coverage:** DJ-149 §1 (verb scope + supersession) → Tasks 1, 2, 3. §2 (preconditions) → Task 15's Step 0. §3 (state surface migration) → Tasks 4, 5. §4 (drift classification) → Tasks 14 (drift-classifier), 15 (playbook Step 2). §5 (master plan; runtime-decided) → Task 15's Steps 3+4. §6 (stacked worktrees + branch naming) → Task 15's Step 4 dispatch prose. §7 (halt on failure) → Task 15's Step 7 verdict. §8 (test-asserted live) → Task 9's buildReconciliationStateRecord + Task 15's Step 4. §9 (per-runtime convergence + max_iterations) → Tasks 16, 17, 19. §10 (DJ-147 dry-run inheritance + overlay extension + 6 new tools) → Tasks 6, 7, 8, 9, 10, 11, 12. All 10 decision points have implementing tasks.

- **Placeholder scan:** No TBD/TODO. Concrete code in every step. Two judgement points the implementer resolves at execution: (a) the exact text of the playbook prose in Task 15 (the doc has the design; implementer adapts to playbook tone matching DJ-148's), (b) how `WorkstreamID` is repurposed as `BranchName` in the state record — Task 9 uses the existing `WorkstreamID` field for the branch name (rather than adding a new field; matches DJ-068's "N approaches share one WorkstreamID" framing where WorkstreamID was a council-era concept retired by DJ-121).

- **Type consistency:** `stateRecordReconciliationInput`/`buildReconciliationStateRecord`/`captureStateRecordReconciliation` consistent. `OverlayPutState`/`OverlayDeleteState`/`GetState`/`ListStateRecords` consistent. `state.ComputeSpecHashes`/`state.BodyGetter`/`SpecStore.BodyBytes` consistent. Six tool names: `state_record_reconciliation`/`state_refresh_artifacts`/`state_mark_status`/`state_delete_record`/`state_list_records`/`state_get_record` consistent across all task references.

- **Known calibrations at execution time:** Task 9's `WorkstreamID`-as-branch_name reuse vs adding a `BranchName` field to `ReconciliationState` (the cleaner alternative if `WorkstreamID` repurposing feels wrong); Task 15's playbook prose length (mirror DJ-148's code_assimilation.md target ~150 lines); Task 19's `cmd/activity_verb.go` exact integration point for the adopt-lock guard (depends on existing `runActivityVerb` shape).
