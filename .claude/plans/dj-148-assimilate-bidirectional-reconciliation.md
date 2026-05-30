# DJ-148 `assimilate` Brownfield Bidirectional Reconciliation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the `code_assimilation` activity playbook that reads brownfield source code, infers/revises features+decisions+strategies (code-is-truth direction), synthesizes approaches binding inferred specs to source files with `source_hash`, and produces coherent (spec, code, approach) state. Closes DJ-135 phase 5 checkpoint 3 leak.

**Architecture:** Extends `spec.Approach` with `SourceFiles`/`SourceHash`/`SourceHashSyncedAt` (the binding shape DJ-149's drift detection consumes). Adds two MCP tools (`spec_propose_approach`, `spec_revise_approach`) wrapped via DJ-147's `captureOnly` for dry-run inheritance. Rewrites five council-era assimilation subagent prompts (scout + backend-/frontend-/infra-analyzer + gap-analyst) for current conventions (axis-shaped ids, no entity persistence, current strategy shape). Authors three playbook tiers (`code_assimilation.md` default, `.claude-code.md` provider overlay, `.interactive.md` mode overlay) following DJ-144's per-runtime convergence-driver pattern.

**Tech Stack:** Go 1.x; `github.com/modelcontextprotocol/go-sdk` v1.6.1; `github.com/coder/acp-go-sdk`; `github.com/stretchr/testify/{assert,require}`. Spec doc: [docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md](../../docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md).

---

## Required reading before starting

- **[docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md](../../docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md)** — the governing spec. Read all 10 decision points + Resolved Questions + Alternatives + Consequences.
- **[docs/decisions/dj-075-assimilate-reads-existing-spec.md](../../docs/decisions/dj-075-assimilate-reads-existing-spec.md)** — the six DJ-075 invariants DJ-148 carries forward (ExistingSpec pre-load, `inferred` status default, matching-ID idempotency).
- **[docs/decisions/dj-087-approaches-are-synthesized-adopt.md](../../docs/decisions/dj-087-approaches-are-synthesized-adopt.md)** — the `app-<parent-id>` id convention DJ-148 inherits.
- **[docs/decisions/dj-147-dry-run-mutation-capture.md](../../docs/decisions/dj-147-dry-run-mutation-capture.md)** — the `captureOnly` wrapper pattern; DJ-148's two new tools mirror it.
- **[docs/agent-conventions.md](../../docs/agent-conventions.md)** — MANDATORY before any prompt edit under `internal/scaffold/agents/`. Walk the six anti-patterns + four positive patterns as a checklist before each rewrite.
- **[internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) + [spec_refinement.claude-code.md](../../internal/scaffold/plans/spec_refinement.claude-code.md) + [spec_refinement.interactive.md](../../internal/scaffold/plans/spec_refinement.interactive.md)** — the three-tier playbook pattern DJ-148 mirrors.

## Phase ordering & independence

- Phase 1 (DJ doc fix + spec data model) is the foundation everything else needs.
- Phase 2 (input types + builder + capture closures) depends on Phase 1.
- Phase 3 (MCP tool registration) depends on Phase 2.
- Phase 4 (activity registry + cmd doc-comment) is independent — can run in parallel with Phases 2–3.
- Phase 5 (subagent prompt rewrites) is independent of Phases 1-4 — pure markdown edits.
- Phase 6 (playbook authoring) depends on Phase 3 (tools must exist) and Phase 5 (subagent ids must be settled).
- Phase 7 (docs) depends on Phases 1-6.
- Phase 8 (validation) last.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:` / `fix:` / `refactor:` / `test:` / `docs:`).

---

## Phase 1 — DJ doc fix + spec data model

### Task 1: Fix DJ-148 doc — Approach storage is YAML frontmatter, not JSON

**Files:**
- Modify: `docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md`

The DJ doc says "on-disk JSON shape under `.borg/spec/approaches/`" in two places (decision point #6 and Consequences). Per `internal/spec/approach.go:12`, approaches are stored as YAML frontmatter + markdown body (`.md` files), not JSON. Fix inline.

- [ ] **Step 1: Read the existing language**

Run: `grep -n 'JSON shape' docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md`
Expected: two hits at decision point #6 and Consequences.

- [ ] **Step 2: Apply the fix**

In `docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md`, replace:
- `The on-disk JSON shape under \`.borg/spec/approaches/\` gains the three new fields; existing approaches without them are loaded with zero values (no migration needed — DJ-148 lands before any approaches exist in any project).`

with:
- `The on-disk YAML-frontmatter shape under \`.borg/spec/approaches/<id>.md\` (per \`internal/spec/approach.go\`'s \`yaml:\` tags) gains the three new fields; existing approaches without them load with zero values (no migration needed — DJ-148 lands before any approaches exist in any project).`

And replace:
- `- On-disk JSON shape under \`.borg/spec/approaches/\` extended; existing approaches (zero today) load with zero values.`

with:
- `- On-disk YAML-frontmatter shape (per \`internal/spec/approach.go\`) extended with three new \`yaml:\` tagged fields; existing approaches (zero today) load with zero values.`

- [ ] **Step 3: Verify the bijection test still passes**

Run: `go test ./internal/docs/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md
git commit -m "docs(dj-148): correct approach storage shape (YAML frontmatter, not JSON)"
```

---

### Task 2: Extend `spec.Approach` with `SourceFiles`, `SourceHash`, `SourceHashSyncedAt`

**Files:**
- Modify: `internal/spec/approach.go`
- Test: `internal/spec/approach_test.go` (create if absent; otherwise append)

- [ ] **Step 1: Write the failing test**

Find or create `internal/spec/approach_test.go`. Append:

```go
package spec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestApproach_SourceBindingFields(t *testing.T) {
	a := Approach{
		ID:                 "app-feat-foo",
		Title:              "Foo",
		ParentID:           "feat-foo",
		SourceFiles:        []string{"internal/foo/foo.go", "internal/foo/foo_test.go"},
		SourceHash:         "sha256:abc123",
		SourceHashSyncedAt: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
	out, err := yaml.Marshal(a)
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "source_files:")
	assert.Contains(t, s, "internal/foo/foo.go")
	assert.Contains(t, s, "source_hash: sha256:abc123")
	assert.Contains(t, s, "source_hash_synced_at:")
}

func TestApproach_SourceBindingOmittedWhenEmpty(t *testing.T) {
	a := Approach{ID: "app-feat-bar", Title: "Bar", ParentID: "feat-bar"}
	out, err := yaml.Marshal(a)
	require.NoError(t, err)
	s := string(out)
	assert.NotContains(t, s, "source_files:")
	assert.NotContains(t, s, "source_hash:")
	assert.NotContains(t, s, "source_hash_synced_at:")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/spec/ -run TestApproach_Source -v`
Expected: FAIL — fields don't exist on the struct.

- [ ] **Step 3: Add the fields**

In `internal/spec/approach.go`, inside the `Approach` struct after the existing `Respects []string` line (around line 54), before `CreatedAt`:

```go
	// SourceFiles lists the source files this approach binds to,
	// relative to the project root. Populated by assimilate (DJ-148)
	// at synthesis time from the code that justified inferring the
	// parent feature/strategy; populated by adopt (DJ-149) on
	// approach synthesis for refine-added parents. Used by adopt's
	// drift detection to compute the current SourceHash for
	// comparison.
	SourceFiles []string `yaml:"source_files,omitempty"`

	// SourceHash is the sha256 hash over the concatenation of
	// (sorted-path-then-content) of each file in SourceFiles, in the
	// form sha256:<hex>. Computed by the agent and supplied to the
	// MCP tool — opaque to the daemon, which just stores it. DJ-149's
	// drift detection re-computes from current file content and
	// compares against this stored value to classify the approach.
	SourceHash string `yaml:"source_hash,omitempty"`

	// SourceHashSyncedAt records when the SourceHash was last
	// computed. Server-stamped on every propose/revise. Useful for
	// audit ("when did we last check this approach against code?")
	// and for surfacing stale-state warnings in adopt's classifier.
	SourceHashSyncedAt time.Time `yaml:"source_hash_synced_at,omitempty"`
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/spec/ -run TestApproach_Source -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Run the broader spec suite to confirm no regression**

Run: `go test ./internal/spec/ -v 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/spec/approach.go internal/spec/approach_test.go
git commit -m "feat(spec): approach gains SourceFiles + SourceHash + SourceHashSyncedAt (DJ-148)"
```

---

## Phase 2 — MCP input types + body builder + capture closures

### Task 3: Define `proposeApproachInput` + `reviseApproachInput` types + descriptions

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (add types + description constants)

- [ ] **Step 1: Add input types and description constants**

In `internal/mcp/tools_spec_write.go`, locate the description constants block near the top (around line 26-30 where `descSpecProposeDecision` etc. live). Add:

```go
const descSpecProposeApproach = "Propose a new approach binding a feature/strategy/bug to the source files that implement it (upsert semantics on id). Input is the full approach body — the server fills created_at + updated_at = now (use spec_revise_approach instead when you need to preserve the original created_at). The id must use the app- prefix and conventionally follows app-<parent-id> per DJ-087 (e.g. app-feat-login-flow). parent_id is required and must reference an existing feature (feat-), strategy (strat-), or bug (bug-) — the parent kind is derived from the prefix. source_files is required and lists the relative paths the approach binds to (every path must exist in the working tree at proposal time). source_hash is required and is the sha256:<hex> hash over the sorted-paths-then-contents of source_files, computed by the agent — opaque to the daemon. Optional citation fields (DJ-139): advances lists goal-* ids; respects lists agoal-* ids. On success the manifest is persisted to .borg/spec/approaches/<id>.md (YAML frontmatter + markdown body) and every subscriber to spec://manifest receives notifications/resources/updated."

const descSpecReviseApproach = "Revise an existing approach (the id MUST already exist; use spec_propose_approach instead to create a new one). Input shape is identical to spec_propose_approach. Preserves the original created_at; the server updates updated_at + source_hash_synced_at to now. The parent_id may be changed (e.g. when a feature is reparented under a different strategy) but the new parent must exist and the prefix dictates the new parent kind."
```

Then locate the existing `proposeFeatureInput` struct (search for `type proposeFeatureInput struct`) and add adjacent to it:

```go
// proposeApproachInput shapes the spec_propose_approach tool input.
// Per DJ-148, source_files + source_hash are required so the
// resulting approach establishes coherent (spec, code, approach)
// state. The parent kind is derived from parent_id's prefix
// (feat- / strat- / bug-).
type proposeApproachInput struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	ParentID    string   `json:"parent_id"`
	Body        string   `json:"body"`
	SourceFiles []string `json:"source_files"`
	SourceHash  string   `json:"source_hash"`
	Decisions   []string `json:"decisions,omitempty"`
	Advances    []string `json:"advances,omitempty"`
	Respects    []string `json:"respects,omitempty"`
}

// reviseApproachInput is structurally identical to proposeApproachInput;
// the existence check and created_at preservation are handled at the
// handler level, not at the type level.
type reviseApproachInput proposeApproachInput
```

- [ ] **Step 2: Verify the file compiles**

Run: `go build ./internal/mcp/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/tools_spec_write.go
git commit -m "feat(mcp): proposeApproachInput + reviseApproachInput types + descriptions (DJ-148)"
```

---

### Task 4: Add `buildApproachBody` helper

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (add `buildApproachBody`)
- Test: `internal/mcp/tools_spec_write_test.go` (append if file exists; otherwise create)

- [ ] **Step 1: Write the failing test**

Append (or create) `internal/mcp/tools_spec_write_test.go`:

```go
package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildApproachBody_PopulatesAllFields(t *testing.T) {
	in := proposeApproachInput{
		ID:          "app-feat-login",
		Title:       "Login flow approach",
		Summary:     "JWT-based with refresh tokens",
		ParentID:    "feat-login",
		Body:        "## Implementation\n\nUse JWT...",
		SourceFiles: []string{"internal/auth/jwt.go", "internal/auth/middleware.go"},
		SourceHash:  "sha256:deadbeef",
		Decisions:   []string{"dec-auth-approach"},
		Advances:    []string{"goal-secure-by-default"},
		Respects:    []string{"agoal-fundraising"},
	}
	body, err := buildApproachBody(in, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "app-feat-login", body.ID)
	assert.Equal(t, "Login flow approach", body.Title)
	assert.Equal(t, "feat-login", body.ParentID)
	assert.Equal(t, []string{"internal/auth/jwt.go", "internal/auth/middleware.go"}, body.SourceFiles)
	assert.Equal(t, "sha256:deadbeef", body.SourceHash)
	assert.False(t, body.CreatedAt.IsZero(), "created_at must be stamped when zero passed")
	assert.False(t, body.UpdatedAt.IsZero(), "updated_at must be stamped")
	assert.False(t, body.SourceHashSyncedAt.IsZero(), "source_hash_synced_at must be stamped")
}

func TestBuildApproachBody_PreservesCreatedAtWhenSupplied(t *testing.T) {
	in := proposeApproachInput{
		ID: "app-feat-bar", Title: "Bar", ParentID: "feat-bar", Body: "x",
		SourceFiles: []string{"f.go"}, SourceHash: "sha256:x",
	}
	original := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body, err := buildApproachBody(in, original)
	require.NoError(t, err)
	assert.Equal(t, original, body.CreatedAt, "created_at must be preserved when non-zero")
	assert.True(t, body.UpdatedAt.After(original) || body.UpdatedAt.Equal(original), "updated_at must be now (>= original)")
}

func TestBuildApproachBody_RejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		in   proposeApproachInput
		want string
	}{
		{"empty id", proposeApproachInput{Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:x"}, "id is required"},
		{"wrong id prefix", proposeApproachInput{ID: "dec-foo", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:x"}, "app- prefix"},
		{"empty title", proposeApproachInput{ID: "app-x", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:x"}, "title is required"},
		{"empty parent_id", proposeApproachInput{ID: "app-x", Title: "T", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:x"}, "parent_id is required"},
		{"bad parent prefix", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "goal-foo", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "sha256:x"}, "parent_id must be a feature"},
		{"empty source_files", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceHash: "sha256:x"}, "source_files is required"},
		{"empty source_hash", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}}, "source_hash is required"},
		{"malformed source_hash", proposeApproachInput{ID: "app-x", Title: "T", ParentID: "feat-x", Body: "b", SourceFiles: []string{"f.go"}, SourceHash: "abc"}, "source_hash must be in sha256:<hex>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildApproachBody(tc.in, time.Time{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/mcp/ -run TestBuildApproachBody -v`
Expected: FAIL — `buildApproachBody` undefined.

- [ ] **Step 3: Add the builder**

In `internal/mcp/tools_spec_write.go`, after `buildFeatureBody` (around line 653), add:

```go
// buildApproachBody assembles a spec.Approach from the input,
// filling server-managed fields. createdAt zero-value means "set
// to now" (propose); non-zero preserves the supplied value
// (revise). SourceHashSyncedAt is always stamped to now since the
// hash is being recorded.
//
// Parent kind is derived from parent_id's prefix (feat- / strat- /
// bug-). Existence validation happens at the handler level; the
// builder only enforces shape.
func buildApproachBody(in proposeApproachInput, createdAt time.Time) (spec.Approach, error) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return spec.Approach{}, fmt.Errorf("id is required")
	}
	if !strings.HasPrefix(id, "app-") {
		return spec.Approach{}, fmt.Errorf("id %q must use the app- prefix per DJ-087's id convention", id)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return spec.Approach{}, fmt.Errorf("title is required")
	}
	parentID := strings.TrimSpace(in.ParentID)
	if parentID == "" {
		return spec.Approach{}, fmt.Errorf("parent_id is required (the feature, strategy, or bug this approach implements)")
	}
	switch {
	case strings.HasPrefix(parentID, "feat-"),
		strings.HasPrefix(parentID, "strat-"),
		strings.HasPrefix(parentID, "bug-"):
		// ok
	default:
		return spec.Approach{}, fmt.Errorf("parent_id must be a feature (feat-), strategy (strat-), or bug (bug-) id; got %q", parentID)
	}
	if len(in.SourceFiles) == 0 {
		return spec.Approach{}, fmt.Errorf("source_files is required and must list at least one path the approach binds to")
	}
	hash := strings.TrimSpace(in.SourceHash)
	if hash == "" {
		return spec.Approach{}, fmt.Errorf("source_hash is required (compute the sha256:<hex> of sorted-paths-then-contents of source_files)")
	}
	if !strings.HasPrefix(hash, "sha256:") || len(hash) < len("sha256:")+8 {
		return spec.Approach{}, fmt.Errorf("source_hash must be in sha256:<hex> form; got %q", hash)
	}
	return spec.Approach{
		ID:                 id,
		Title:              title,
		Summary:            strings.TrimSpace(in.Summary),
		ParentID:           parentID,
		Body:               in.Body,
		SourceFiles:        in.SourceFiles,
		SourceHash:         hash,
		SourceHashSyncedAt: now,
		Decisions:          in.Decisions,
		Advances:           in.Advances,
		Respects:           in.Respects,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
	}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/mcp/ -run TestBuildApproachBody -v`
Expected: PASS (3 test functions, 8 subtests in the table).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_test.go
git commit -m "feat(mcp): buildApproachBody helper with shape validation (DJ-148)"
```

---

### Task 5: Add `captureProposeApproach` + `captureReviseApproach` closures + `existingApproachCreatedAt` helper

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (add capture closures + the createdAt-preserve helper)

The closures land DJ-147 dry-run capture for the two new tools. Mirrors the existing `captureProposeDecision` / `captureReviseDecision` pattern from DJ-147 Task 5.

- [ ] **Step 1: Locate the existing capture closure region**

Run: `grep -nE 'func capture(Propose|Revise|Mark)' internal/mcp/tools_spec_write.go | head -5`
Expected: list of existing capture closures around line 870+.

- [ ] **Step 2: Add the existing-createdAt helper**

At the end of `internal/mcp/tools_spec_write.go` (near the other `existing*CreatedAt` family if present, or with the capture closures), add:

```go
// existingApproachCreatedAt reads an approach's CreatedAt from the
// overlay-view (overlay-first, base-store-fallback) so the
// captureReviseApproach closure preserves the original timestamp
// under both dry-run and non-dry-run sessions.
func existingApproachCreatedAt(store *agent.SpecStore, sess *mcp.ServerSession, id string) (time.Time, bool) {
	view := store.OverlayView(sess)
	entry, ok := view.Lookup(agent.KindApproach, id)
	if !ok {
		return time.Time{}, false
	}
	a, ok := entry.Body.(spec.Approach)
	if !ok {
		return time.Time{}, false
	}
	return a.CreatedAt, true
}
```

- [ ] **Step 3: Add the two capture closures**

In the same file's capture-closure region (where `captureProposeDecision` etc. live), add:

```go
// captureProposeApproach is the dry-run twin of the
// spec_propose_approach handler. Same input validation as the
// production path; instead of commitOne it calls OverlayPut. The
// same body shape SpecStore.Put would have persisted lands in the
// overlay so subsequent reads in this session see it.
func captureProposeApproach(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeApproachInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeApproachInput) (any, error) {
		body, err := buildApproachBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_approach", agent.KindApproach, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

// captureReviseApproach preserves the existing CreatedAt via the
// overlay-aware helper so revise-after-propose within the same
// dry-run session preserves the would-be created_at.
func captureReviseApproach(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseApproachInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseApproachInput) (any, error) {
		// existence check via overlay-view
		view := store.OverlayView(sess)
		_, ok := view.Lookup(agent.KindApproach, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_approach: approach %q does not exist; use spec_propose_approach to create it", in.ID)
		}
		createdAt, _ := existingApproachCreatedAt(store, sess, in.ID)
		body, err := buildApproachBody(proposeApproachInput(in), createdAt)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_approach", agent.KindApproach, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}
```

- [ ] **Step 4: Verify the file compiles**

Run: `go build ./internal/mcp/...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_spec_write.go
git commit -m "feat(mcp): captureProposeApproach + captureReviseApproach closures (DJ-148)"
```

---

## Phase 3 — MCP tool registration

### Task 6: Register `spec_propose_approach` + handler

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (registration)
- Test: append `internal/mcp/tools_spec_write_dry_run_test.go` (extend DJ-147 table)

- [ ] **Step 1: Locate the right registration site**

The new tool's registration goes alongside the other `spec_propose_*` registrations. Run: `grep -nE 'Name: *"spec_propose_strategy"' internal/mcp/tools_spec_write.go` to find the last propose registration; insert the new approach registration after it.

- [ ] **Step 2: Add the registration**

In `internal/mcp/tools_spec_write.go`, after the `spec_propose_strategy` registration block, add:

```go
mcp.AddTool(server, &mcp.Tool{
	Name:        "spec_propose_approach",
	Description: descSpecProposeApproach,
}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeApproachInput) (*mcp.CallToolResult, any, error) {
	// Validate parent_id exists in the manifest before committing —
	// the builder validates the prefix shape; the handler validates
	// presence.
	parent := strings.TrimSpace(in.ParentID)
	if parent != "" {
		res := store.GetSpec([]string{parent})
		if entry, ok := res.Results[parent]; !ok || entry.Status == agent.SpecGetMissing {
			return errorResult(fmt.Sprintf("spec_propose_approach: parent_id %q does not exist in the manifest; create the parent before binding an approach to it", parent)), nil, nil
		}
	}
	body, err := buildApproachBody(in, time.Time{})
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if err := commitOne(store, agent.KindApproach, in.ID, body); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	publishManifestUpdate(ctx, server)
	return textResult(fmt.Sprintf("Proposed approach %s under %s (binds %d files; source_hash %s).", body.ID, body.ParentID, len(body.SourceFiles), body.SourceHash)), nil, nil
}, captureProposeApproach(store)))
```

- [ ] **Step 3: Append a dry-run table subtest**

In `internal/mcp/tools_spec_write_dry_run_test.go`, find the `TestDryRunCapturesMutations` table (the `cases := []struct{...}` block). Add a new entry to the table:

```go
{"propose_approach", "spec_propose_approach",
	map[string]any{
		"id":           "app-feat-foo",
		"title":        "Foo approach",
		"parent_id":    "feat-foo",
		"body":         "## Implementation\n\nThe foo flow.",
		"source_files": []string{"internal/foo/foo.go"},
		"source_hash":  "sha256:deadbeef00",
	},
	agent.KindApproach, "app-feat-foo"},
```

The table-driven test will exercise the new tool through the in-memory MCP transport.

- [ ] **Step 4: Run the dry-run test to verify it fails for the new entry**

Run: `go test ./internal/mcp/ -run 'TestDryRunCapturesMutations/propose_approach' -v`
Expected: FAIL — propose_approach tool not registered (the run above already adds the registration; if you appended the test before the registration, this fails; if you run both together, this passes).

- [ ] **Step 5: Run the full dry-run capture suite to confirm**

Run: `go test ./internal/mcp/ -run 'TestDryRunCapturesMutations' -v`
Expected: PASS (existing entries + new propose_approach entry).

- [ ] **Step 6: Run the full mcp suite**

Run: `go test ./internal/mcp/ -race 2>&1 | tail -10`
Expected: all pass under race.

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_dry_run_test.go
git commit -m "feat(mcp): register spec_propose_approach with captureOnly (DJ-148)"
```

---

### Task 7: Register `spec_revise_approach` + handler

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` (registration)
- Test: extend the dry-run table same way

- [ ] **Step 1: Add the registration**

In `internal/mcp/tools_spec_write.go`, after the `spec_propose_approach` registration block, add:

```go
mcp.AddTool(server, &mcp.Tool{
	Name:        "spec_revise_approach",
	Description: descSpecReviseApproach,
}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseApproachInput) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return errorResult("spec_revise_approach: id is required"), nil, nil
	}
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return errorResult(fmt.Sprintf("spec_revise_approach: approach %q does not exist; use spec_propose_approach to create it", id)), nil, nil
	}
	existing, ok := entry.Body.(spec.Approach)
	if !ok {
		return errorResult(fmt.Sprintf("spec_revise_approach: %q resolved to %T, not spec.Approach", id, entry.Body)), nil, nil
	}
	parent := strings.TrimSpace(in.ParentID)
	if parent != "" {
		pres := store.GetSpec([]string{parent})
		if pentry, ok := pres.Results[parent]; !ok || pentry.Status == agent.SpecGetMissing {
			return errorResult(fmt.Sprintf("spec_revise_approach: parent_id %q does not exist in the manifest", parent)), nil, nil
		}
	}
	body, err := buildApproachBody(proposeApproachInput(in), existing.CreatedAt)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if err := commitOne(store, agent.KindApproach, body.ID, body); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	publishManifestUpdate(ctx, server)
	return textResult(fmt.Sprintf("Revised approach %s (source_hash %s).", body.ID, body.SourceHash)), nil, nil
}, captureReviseApproach(store)))
```

- [ ] **Step 2: Extend the dry-run revise table**

In `internal/mcp/tools_spec_write_dry_run_test.go`, find the table that exercises revise tools (`TestDryRunCapturesRevise` or similar). Add an entry for `propose+revise_approach` similar to the existing per-kind revise entries — seed an approach via Put, then call `spec_revise_approach` and assert overlay state. Mirror the existing `revise_decision` / `revise_feature` test entries' shape.

If the existing test file uses a different shape (e.g., the revise tests aren't table-driven), add a standalone test function:

```go
func TestDryRunCapturesReviseApproach(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed the parent feature + the approach in the base store
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))
	require.NoError(t, store.Put(agent.KindApproach, "app-feat-foo", spec.Approach{
		ID: "app-feat-foo", Title: "Old title", ParentID: "feat-foo",
		Body: "old", SourceFiles: []string{"f.go"}, SourceHash: "sha256:old00",
	}, agent.OriginSettled))

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
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

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_approach",
		Arguments: map[string]any{
			"id":           "app-feat-foo",
			"title":        "New title",
			"parent_id":    "feat-foo",
			"body":         "new",
			"source_files": []string{"f.go", "g.go"},
			"source_hash":  "sha256:new00",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Captured in overlay
	caps := store.OverlayCaptured(ss)
	require.Len(t, caps, 1)
	assert.Equal(t, "spec_revise_approach", caps[0].Tool)
	assert.Equal(t, "app-feat-foo", caps[0].ID)
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/mcp/ -run 'TestDryRunCapturesReviseApproach|TestDryRunCapturesMutations' -v`
Expected: PASS.

- [ ] **Step 4: Run the full mcp suite under race**

Run: `go test ./internal/mcp/ -race 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_dry_run_test.go
git commit -m "feat(mcp): register spec_revise_approach with captureOnly (DJ-148)"
```

---

### Task 8: End-to-end approach mutation test (non-dry-run path)

**Files:**
- Test: append `internal/mcp/tools_spec_write_test.go`

Validates the non-dry-run production path persists to disk correctly with the new fields.

- [ ] **Step 1: Append the test**

In `internal/mcp/tools_spec_write_test.go`:

```go
func TestProposeApproach_PersistsToDisk(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	// seed parent
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_approach",
		Arguments: map[string]any{
			"id":           "app-feat-foo",
			"title":        "Foo approach",
			"parent_id":    "feat-foo",
			"body":         "## Implementation",
			"source_files": []string{"internal/foo/foo.go"},
			"source_hash":  "sha256:abcd1234",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "result: %+v", res)

	// file persisted with new fields
	data, err := fsys.ReadFile(".borg/spec/approaches/app-feat-foo.md")
	require.NoError(t, err)
	contents := string(data)
	assert.Contains(t, contents, "source_files:")
	assert.Contains(t, contents, "internal/foo/foo.go")
	assert.Contains(t, contents, "source_hash: sha256:abcd1234")
	assert.Contains(t, contents, "source_hash_synced_at:")
}

func TestProposeApproach_RejectsMissingParent(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, activity.DefaultRegistry(), nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_approach",
		Arguments: map[string]any{
			"id":           "app-feat-missing",
			"title":        "Missing parent",
			"parent_id":    "feat-missing",
			"body":         "x",
			"source_files": []string{"f.go"},
			"source_hash":  "sha256:xxxxxxxx",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/mcp/ -run 'TestProposeApproach' -v`
Expected: PASS (2 tests).

- [ ] **Step 3: Full mcp suite under race**

Run: `go test ./internal/mcp/ -race`
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools_spec_write_test.go
git commit -m "test(mcp): end-to-end propose_approach persists with new fields (DJ-148)"
```

---

## Phase 4 — Activity registry + cmd doc-comment

### Task 9: Set `code_assimilation` `max_iterations: 3` + cmd doc refresh

**Files:**
- Modify: `internal/activity/agents-default.yaml`
- Modify: `cmd/assimilate.go` (doc-comment refresh)

- [ ] **Step 1: Update the activity registry**

In `internal/activity/agents-default.yaml`, find the `code_assimilation:` entry (currently has `max_iterations: 20`) and change to:

```yaml
  code_assimilation:
    runtimes:
      - claude-code
      - codex
      - gemini
    max_iterations: 3
```

- [ ] **Step 2: Refresh `cmd/assimilate.go` doc-comment**

In `cmd/assimilate.go`, replace the existing doc-block on `AssimilateCmd` with:

```go
// AssimilateCmd dispatches the code_assimilation activity (DJ-148).
// The activity reads brownfield source code, infers/revises
// features+decisions+strategies (code-is-truth direction),
// synthesizes approaches binding inferred specs to source files
// with source_hash, and produces coherent (spec, code, approach)
// state.
//
// Preconditions checked by the playbook in Step 0 (refused with a
// helpful error when missing):
//   - GOALS.md exists in the working tree
//   - Goal layer is populated (operator ran `locutus refine goals` first)
//
// The --dry-run + --format flags inherit from DJ-147; mutations
// capture in the per-session overlay via the captureOnly wrapper
// at the MCP tool boundary.
type AssimilateCmd struct {
	DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
	Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}
```

- [ ] **Step 3: Verify the build**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 4: Verify the registry parses**

Run: `go test ./internal/activity/ -v 2>&1 | tail -10`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/activity/agents-default.yaml cmd/assimilate.go
git commit -m "feat(activity): code_assimilation max_iterations=3 + cmd doc refresh (DJ-148)"
```

---

## Phase 5 — Subagent prompt rewrites

> **MANDATORY: Read [docs/agent-conventions.md](../../docs/agent-conventions.md) before starting any rewrite in this phase.** Walk the six anti-patterns + four positive patterns as a checklist before each task. Per the user's [feedback-agent-conventions-checklist-first](../../docs/agent-conventions.md) preference, "I know the conventions" is not enough — actually walk the checklist for each prompt.

The five prompt rewrites in this phase share a uniform set of changes:

**Drop:**
- `output_schema: AssimilationContribution` from the front-matter (council-era typed contract; not enforced in activity-playbook model)
- Council-era ID prefixes (`d-`, `s-`, `e-`)
- Entity emission (per DJ-076 + DJ-148 decision #4)
- Council-era strategy shape (build/test commands with `commands` map + `governs` globs)

**Add / update:**
- Current ID conventions (DJ-133: `dec-<axis>`; current: `feat-<slug>`, `strat-<slug>`, `app-<parent-id>`)
- Current `spec.Strategy` shape (forward-looking implementation approach)
- MCP tool references for state mutations (`mcp__locutus__spec_propose_*` / `spec_revise_*`)
- Reference to DJ-148 governing decision

**Keep:**
- Each agent's domain expertise + analysis methodology
- Confidence calibration rules (where present)
- Evidence-citation requirements

### Task 10: Rewrite `internal/scaffold/agents/scout.md`

**Files:**
- Modify: `internal/scaffold/agents/scout.md`

The scout's purpose: enumerate languages, frameworks, component boundaries, structure, config files. Emits a `ScoutSummary` the analyzers consume.

- [ ] **Step 1: Walk the agent-conventions checklist**

Read `docs/agent-conventions.md` end-to-end. Note the six anti-patterns.

- [ ] **Step 2: Read the existing scout.md**

Run: `cat internal/scaffold/agents/scout.md`

- [ ] **Step 3: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: scout
thinking: off
role: assimilation-survey
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
---
```

Adjust the body to specify:
- Purpose: survey the project's structure, identify languages/frameworks/components, detect multi-component polyglot layouts
- Inputs: a working-tree path; the manifest's existing spec node ids + goal layer for context
- Output: a structured ScoutSummary the analyzers (backend / frontend / infra) consume. The summary has:
  - `languages: [{language, version_hint, evidence: file_path}]`
  - `frameworks: [{framework, domain: "backend"|"frontend"|"infra", evidence}]`
  - `components: [{name, root_path, primary_language, indicators: [...]}]` — distinct outputs (services, libs, frontends)
  - `entry_points: [path]`
  - `config_files: [path]`
- Anti-hallucination: every entry MUST cite file evidence; if evidence is thin, surface lower confidence
- The scout does NOT propose or revise spec nodes; that's the analyzers' + gap-analyst's job

The detailed prose is the existing scout's, with terminology updated for current conventions. Don't invent new schema fields beyond what the analyzers actually consume.

- [ ] **Step 4: Verify the file still parses as YAML+markdown**

Run: `head -20 internal/scaffold/agents/scout.md`
Expected: valid front-matter block followed by markdown.

- [ ] **Step 5: Run the hyphenated-ids invariant test**

Run: `go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS (the scout's id is `scout` — hyphenated trivially).

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/agents/scout.md
git commit -m "refactor(agents): rewrite scout for current conventions (DJ-148)"
```

---

### Task 11: Rewrite `internal/scaffold/agents/backend-analyzer.md`

**Files:**
- Modify: `internal/scaffold/agents/backend-analyzer.md`

- [ ] **Step 1: Walk the agent-conventions checklist** (per Phase 5 preamble)

- [ ] **Step 2: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: backend-analyzer
thinking: off
role: backend-assimilation-analysis
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
---
```

In the body:
- Replace `d-` decision-id examples with `dec-<axis>` (DJ-133). Example: `d-lang-go` → `dec-backend-language` (with chosen_option `go-1.22`).
- Replace `s-` strategy-id examples with `strat-<slug>`. Example: `s-build-go` → drop entirely (council-era build-command-as-strategy is gone); replace with current-shape examples like `strat-event-sourcing`, `strat-jwt-auth`, `strat-graphql-federation`.
- Drop entity emission entirely (the "## 3. Entities" section). Replace with a brief note: "Domain entities are NOT persisted in the post-DJ-135 model (per DJ-076); skip entity extraction. Concentrate on decisions + strategies + features that explain the code."
- Add a features section: "## 3. Features — user-visible capabilities. Identifier `feat-<slug>` (e.g. `feat-user-auth`, `feat-data-export`, `feat-rate-limiting`). Each feature has a title, summary, description, acceptance_criteria (derived from evidence), and decisions[] listing the decisions it depends on."
- Update the "Output Format" reference to describe the output as a markdown structured response with three sections (Decisions / Strategies / Features), not a typed `AssimilationContribution` JSON.
- Confidence calibration rules: keep as-is.

The methodology, the source-file inspection guidance, and the confidence-calibration tables are valuable — keep all of those, just align the schema references.

- [ ] **Step 3: Verify the file parses**

Run: `head -20 internal/scaffold/agents/backend-analyzer.md`
Expected: valid front-matter.

- [ ] **Step 4: Run the hyphenated-ids invariant test**

Run: `go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/agents/backend-analyzer.md
git commit -m "refactor(agents): rewrite backend-analyzer for current conventions (DJ-148)"
```

---

### Task 12: Rewrite `internal/scaffold/agents/frontend-analyzer.md`

**Files:**
- Modify: `internal/scaffold/agents/frontend-analyzer.md`

Same uniform changes as Task 11, applied to the frontend domain.

- [ ] **Step 1: Walk the agent-conventions checklist**

- [ ] **Step 2: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: frontend-analyzer
thinking: off
role: frontend-assimilation-analysis
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
---
```

In the body:
- Keep the "Early exit: no frontend detected" section — this guard is load-bearing.
- Update decision-id examples: `d-frontend-framework` → `dec-frontend-framework`; `d-state-management` → `dec-state-management`; etc.
- Update strategy-id examples to current shape: e.g. `strat-server-side-rendering`, `strat-component-driven-design`.
- Drop entity emission (same note as backend-analyzer).
- Add features section: "## 3. Features — user-visible UI capabilities. Identifier `feat-<slug>` (e.g. `feat-dashboard`, `feat-search-bar`, `feat-notifications-panel`)."
- Output Format: structured markdown with Decisions / Strategies / Features sections.

- [ ] **Step 3: Verify**

Run: `go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/agents/frontend-analyzer.md
git commit -m "refactor(agents): rewrite frontend-analyzer for current conventions (DJ-148)"
```

---

### Task 13: Rewrite `internal/scaffold/agents/infra-analyzer.md`

**Files:**
- Modify: `internal/scaffold/agents/infra-analyzer.md`

Same uniform changes, infra domain.

- [ ] **Step 1: Walk the agent-conventions checklist**

- [ ] **Step 2: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: infra-analyzer
thinking: off
role: infra-assimilation-analysis
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
---
```

In the body:
- Update decision-id examples to `dec-deployment-target`, `dec-ci-platform`, `dec-container-runtime`, `dec-orchestrator`, etc.
- Update strategy-id examples to current shape: `strat-blue-green-deploy`, `strat-immutable-infrastructure`, `strat-multi-region`.
- Drop entity emission (note).
- Add features section: infra features tend to be operational capabilities — `feat-rolling-deploys`, `feat-multi-region-failover`, `feat-canary-releases`, `feat-secret-rotation`.
- Output Format: structured markdown.

- [ ] **Step 3: Verify**

Run: `go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/agents/infra-analyzer.md
git commit -m "refactor(agents): rewrite infra-analyzer for current conventions (DJ-148)"
```

---

### Task 14: Rewrite `internal/scaffold/agents/gap-analyst.md`

**Files:**
- Modify: `internal/scaffold/agents/gap-analyst.md`

Gap-analyst is the reconciler — receives the three analyzer contributions + the current manifest, decides per-node whether to confirm, revise, or propose.

- [ ] **Step 1: Walk the agent-conventions checklist**

- [ ] **Step 2: Apply the rewrite**

Replace the front-matter to drop `output_schema:`:

```yaml
---
id: gap-analyst
thinking: on
role: spec-code-reconciliation
models:
  - {provider: anthropic, tier: balanced}
  - {provider: googleai, tier: balanced}
  - {provider: openai, tier: balanced}
---
```

`thinking: on` because gap-analyst makes per-node reconciliation decisions that benefit from deliberation.

In the body, reframe the agent's job (preserving existing analytical guidance):

- **Purpose**: receive merged analyzer contributions (decisions/strategies/features) + the current manifest's existing nodes + the goal-layer bodies for context. For each contribution, decide one of three actions:
  1. **Confirm**: existing manifest node matches the contribution. No mutation. Note in the report.
  2. **Revise**: existing manifest node exists for the contribution's id but disagrees with what the code shows. Per DJ-148's code-is-truth direction, emit a revision (call the corresponding `mcp__locutus__spec_revise_*` MCP tool with the updated body). Cite the disagreement in the rationale.
  3. **Propose**: no existing manifest node for the contribution's id. Emit a new node (call the corresponding `mcp__locutus__spec_propose_*` MCP tool) with `status: inferred`.

- **Approach synthesis**: for every confirmed-or-newly-proposed feature/strategy, also call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach` for existing approaches) to bind it to the source files that justified inferring it. The approach body includes `source_files` (relative paths the parent binds to) and `source_hash` (sha256 over sorted-paths-then-contents). Hash is computed via Bash: `find <paths> | sort | xargs sha256sum | sha256sum | cut -d' ' -f1` prefixed with `sha256:`.

- **Conflict resolution direction**: code is the truth for assimilate. When existing spec disagrees with what code shows, the revision lands. Surface every revision in the report so the operator can spot intent-vs-reality divergence. Edge cases (aspirational specs the operator hasn't migrated to yet) land as low-confidence revisions; operators reject by re-editing post-run.

- **Report**: the gap-analyst's final output is a structured report listing every reconciliation decision (kind: confirm/revise/propose, id, evidence summary, confidence). The orchestrator reads this to execute the MCP calls.

- [ ] **Step 3: Verify**

Run: `go test ./internal/scaffold/agents/ -run HyphenatedIds -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/agents/gap-analyst.md
git commit -m "refactor(agents): rewrite gap-analyst for current conventions + code-is-truth reconciliation (DJ-148)"
```

---

## Phase 6 — Playbook authoring

### Task 15: Author `internal/scaffold/plans/code_assimilation.md` (default fallback)

**Files:**
- Modify: `internal/scaffold/plans/code_assimilation.md` (currently a 19-line placeholder; rewrite entirely)

The default playbook is the codex/gemini headless shape — single iteration, emit `converged:` verdict at the end. Mirror the structural pattern of `spec_refinement.md` (one-iteration shape, plan-first preamble, manifest-as-source-of-truth, invariants block, step-by-step body, verdict).

- [ ] **Step 1: Replace the placeholder with the full playbook**

Replace the entire contents of `internal/scaffold/plans/code_assimilation.md` with:

```markdown
# Code assimilation playbook (one iteration)

You are the orchestrator of one iteration of code assimilation for a Locutus-managed project. Your job: read the brownfield source code, infer/revise features + decisions + strategies (code-is-truth direction), synthesize approaches binding them to source files (with `source_hash` for DJ-149's drift detection), and produce coherent (spec, code, approach) state per DJ-148. The harness owns the outer loop; your job is to do this iteration well and emit the convergence verdict.

## Plan first

Your very first action this iteration is to call `TodoWrite` (or your runtime's equivalent plan tool) with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers these step labels in order: Precondition check (Step 0), Discover code (Step 1), Read manifest (Step 2), Scout survey (Step 3), Analyzer fan-out (Step 4), Reconciliation (Step 5), Emit + approach synthesis (Step 6), Report verdict (Step 7).

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments). The manifest carries every node + the `goals_md_hash` field. The graph lives in the MCP server; reach for it via tools, not by reading `.borg/spec/` files.

## What you have

- **MCP tools** (server `locutus`, exposed in your tool catalogue with the `mcp__locutus__` prefix):
  - `mcp__locutus__spec_list_manifest` — compact index of every node. Start your iteration here.
  - `mcp__locutus__spec_get` — batched body fetch. Input `{ids: [...]}`. Pass every id you need in one call.
  - `mcp__locutus__spec_search` — ranked free-text search.
  - `mcp__locutus__spec_propose_decision` / `spec_revise_decision` — decision mutations. Per DJ-133 the id equals `dec-<axis-id>`.
  - `mcp__locutus__spec_propose_feature` / `spec_revise_feature` — feature mutations.
  - `mcp__locutus__spec_propose_strategy` / `spec_revise_strategy` — strategy mutations.
  - `mcp__locutus__spec_propose_approach` / `spec_revise_approach` — approach mutations (DJ-148). Required fields: id (`app-` prefix), parent_id (an existing `feat-` / `strat-` / `bug-` id), source_files (relative paths), source_hash (`sha256:<hex>`).
- **Tools the runtime ships** you reach for directly:
  - `Read` — read `GOALS.md` for context.
  - `Bash` — run `git ls-files`, compute sha256 hashes for `source_hash`, inspect file contents.
  - `Task` — dispatch subagents.
- **Subagents** (use the `Task` tool to dispatch one, naming by its hyphenated id):
  - `scout` — surveys the codebase structure. Identifies languages, frameworks, component boundaries.
  - `backend-analyzer` — emits decisions/strategies/features for the backend domain.
  - `frontend-analyzer` — same for frontend. Early-exits if no frontend detected.
  - `infra-analyzer` — same for infra/CI/deployment.
  - `gap-analyst` — receives merged analyzer contributions + current manifest, reconciles per node, emits the per-id action plan (confirm / revise / propose).

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). Direct file writes bypass coherence (the in-process `SpecStore`, the write-through search index, the history events, per-runtime tool policy).
- **`GOALS.md` is read-only.** assimilate reads the goal layer (via `spec_list_manifest` + `spec_get`) as context for grounding inferences. Never call `Write`/`Edit` on `GOALS.md`. Goal-layer node mutations are operator's responsibility via `refine` — assimilate does not propose/revise/delete goal-layer nodes.

## Step 0 — Precondition check

assimilate requires (a) `GOALS.md` exists in the working tree and (b) the goal layer is populated. Both are checked here; refuse with a clear actionable error if either is missing.

1. **Read `GOALS.md` via the `Read` tool.** If the file is missing or empty, emit the verdict line below and stop:
   - `converged: true` followed by a single-line note in your report body: `precondition not met — GOALS.md missing or empty. Run \`locutus init\` to scaffold a template, then edit it with your project's mission statement before running assimilate.`
2. **Inspect the manifest's `Goals` and `AntiGoals` arrays.** If both are empty, emit the verdict and stop:
   - `converged: true` with the note: `precondition not met — goal layer is empty. Run \`locutus refine goals\` first to materialize the goal layer from GOALS.md.`
3. **If both preconditions hold, mark Step 0 complete and proceed to Step 1.**

## Step 1 — Discover code

Run `git ls-files` via the `Bash` tool. Apply hardcoded excludes:
- `.borg/**`
- `.locutus/**`
- `GOALS.md`
- `docs/**` (project documentation — operator-owned, not implementation)
- `**/README.md`, `**/CHANGELOG.md`

The remaining set is the source code surface assimilate considers. Note its size (count of files); if >5000 files, surface a warning in the report ("large codebase; assimilate may take multiple iterations to converge").

## Step 2 — Read manifest + goal layer for context

Call `mcp__locutus__spec_list_manifest` (already done in "Start here"). Then issue one batched `mcp__locutus__spec_get` with every `goal-*` and `agoal-*` id from the Goals/AntiGoals arrays. The goal-layer bodies feed the scout's context — they ground feature/decision/strategy inferences in project intent.

Also fetch every existing `feat-`, `dec-`, `strat-`, `app-` id's body via `spec_get` so the analyzers can distinguish "this would be new" from "this exists; check if it matches".

## Step 3 — Scout survey

Dispatch `scout` (via the `Task` tool) with:
- The discovered source file list (from Step 1).
- The goal-layer bodies (from Step 2).
- A note that this is a brownfield assimilation pass per DJ-148 — the scout's job is to enumerate languages, frameworks, components, structure, NOT to propose spec nodes.

The scout returns a ScoutSummary. Pass it to the analyzers in Step 4.

## Step 4 — Analyzer fan-out

Dispatch the three domain analyzers in parallel (use the `Task` tool — three concurrent dispatches):
- `backend-analyzer` with the scout summary + every file in source roots the scout marked `backend` or component-primary-language matching a backend language.
- `frontend-analyzer` with the scout summary + every frontend file. The frontend-analyzer early-exits if scout's summary lists no frontend indicators.
- `infra-analyzer` with the scout summary + every infrastructure file (Dockerfile, CI configs, deployment manifests, etc.).

Each returns its structured response (Decisions / Strategies / Features sections). Collect all three.

## Step 5 — Reconciliation via gap-analyst

Dispatch `gap-analyst` with:
- The three analyzer contributions (concatenated by section: all decisions, all strategies, all features).
- The current manifest's existing feature/decision/strategy ids and bodies (fetched in Step 2).
- The goal-layer bodies.

The gap-analyst returns a per-id action plan: for each contributed node, one of {confirm, revise, propose}, with rationale and evidence. Code-is-truth resolution: when contributions disagree with existing manifest nodes, the revision lands.

## Step 6 — Emit + approach synthesis

For each gap-analyst-decided action:
- **Confirm**: no MCP call required. Note in the report.
- **Revise**: call the appropriate `mcp__locutus__spec_revise_<kind>` tool with the revised body. Status stays `inferred` for assimilate-originated revisions.
- **Propose**: call the appropriate `mcp__locutus__spec_propose_<kind>` tool with `status: inferred`.

For every feature or strategy that was confirmed-or-newly-proposed, also synthesize an approach binding it to the source files that justified inferring it:

1. Identify the source files the analyzer cited as evidence for this feature/strategy.
2. Compute the `source_hash`: `find <those-files> -type f | sort | xargs sha256sum | sha256sum | cut -d' ' -f1` via the `Bash` tool; prefix the hex with `sha256:`.
3. Call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach` for existing approaches) with id `app-<parent-id>` per DJ-087, the parent's id as `parent_id`, the source files as `source_files`, and the computed hash as `source_hash`. The approach body is a brief markdown brief naming what the code currently does to satisfy the parent.

## Step 7 — Convergence verdict

Emit the verdict line as the last line of your output:

- **`converged: true`** after a successful single pass — including the precondition-failed branches in Step 0 (those are "nothing to do" outcomes; the harness should not re-dispatch).
- **`converged: false; <reason>`** only when re-dispatch would naturally make progress: transient LLM partial output, analyzer subprocess crash mid-fan-out. NOT for analyzer disagreements or operator-actionable issues — those land as notes in the report body with `converged: true`.
```

- [ ] **Step 2: Verify the file parses as markdown**

Run: `head -20 internal/scaffold/plans/code_assimilation.md`
Expected: heading + readable structure.

- [ ] **Step 3: Verify nothing breaks**

Run: `go build ./... && go test ./internal/scaffold/plans/...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/plans/code_assimilation.md
git commit -m "feat(plans): author code_assimilation default playbook (DJ-148)"
```

---

### Task 16: Author `internal/scaffold/plans/code_assimilation.claude-code.md` (provider overlay)

**Files:**
- Create: `internal/scaffold/plans/code_assimilation.claude-code.md`

The Claude Code overlay drives the analyzer fan-out as a dynamic workflow per DJ-144. Mirror the structural shape of `spec_refinement.claude-code.md`.

- [ ] **Step 1: Create the file**

Write to `internal/scaffold/plans/code_assimilation.claude-code.md`:

```markdown
# Code Assimilation (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the brownfield assimilation work for this project to convergence. The workflow loops until the gap-analyst reports no new mutations or the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a single `converged:` verdict line for an outer harness — this workflow is the harness.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers: Precondition check (preamble), then per-iteration: Discover code, Read manifest, Scout survey, Analyzer fan-out, Reconciliation, Emit + approach synthesis — repeated up to {{max_iterations}} times — then Report.

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments) to read the current spec graph state. The manifest carries every node — including `goal-*` and `agoal-*` ids — and the `goals_md_hash` field. Then read `GOALS.md` once with the `Read` tool. Both are inputs to the preamble.

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). The daemon owns coherence.
- **`GOALS.md` is read-only for the duration of this run.** Read it once with the `Read` tool when the playbook says to. Never call `Write` or `Edit` on `GOALS.md`. The goal layer is operator-authored via `refine`.

## One-time preamble — Precondition check (runs once, before the loop)

assimilate requires (a) `GOALS.md` exists and (b) goal layer is populated. Check both. If either fails:
- Emit a single closing message naming the unmet precondition and the operator's fix.
- Do not enter the workflow loop.

If both preconditions hold, mark the preamble plan entry complete and enter the convergence loop.

## Convergence loop (up to {{max_iterations}} iterations)

Each iteration runs Steps 1-6 in order. The loop exits early when the gap-analyst's reconciliation plan is empty (no confirms, no revises, no proposes) — that's convergence. The iteration cap is the safety bound.

### Step 1 — Discover code

Run `git ls-files` via the `Bash` tool. Apply hardcoded excludes (`.borg/**`, `.locutus/**`, `GOALS.md`, `docs/**`, `**/README.md`, `**/CHANGELOG.md`). The remaining set is the source surface.

### Step 2 — Read manifest + goal layer for context

Call `mcp__locutus__spec_list_manifest`. Issue one batched `mcp__locutus__spec_get` with every `goal-*` / `agoal-*` / `feat-` / `dec-` / `strat-` / `app-` id.

### Step 3 — Scout survey (sequential)

Dispatch `scout` with the source file list + goal-layer bodies. Wait for it to return its ScoutSummary.

### Step 4 — Analyzer fan-out (parallel)

Spawn three concurrent subagent dispatches in the workflow:
- `backend-analyzer` with scout summary + backend files
- `frontend-analyzer` with scout summary + frontend files (early-exits if none)
- `infra-analyzer` with scout summary + infra files

Wait for all three to complete. Collect their structured responses.

### Step 5 — Reconciliation (sequential)

Dispatch `gap-analyst` with the three analyzer contributions + the existing manifest's nodes + the goal-layer bodies. It returns the per-id action plan.

### Step 6 — Emit + approach synthesis

For each gap-analyst action:
- **Confirm**: no MCP call.
- **Revise**: call `mcp__locutus__spec_revise_<kind>` with the revised body; status stays `inferred`.
- **Propose**: call `mcp__locutus__spec_propose_<kind>` with `status: inferred`.

For every feature or strategy confirmed-or-proposed, also synthesize the approach:
1. Compute `source_hash` for the cited files: `find <files> -type f | sort | xargs sha256sum | sha256sum | cut -d' ' -f1`, prefix with `sha256:`.
2. Call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach`) with id `app-<parent-id>`, parent_id, source_files, source_hash, body.

If the gap-analyst's action plan was empty AND no approaches were synthesized this iteration, emit `converged` and exit the loop. Otherwise loop to Step 1.

## Closing report

After the loop exits (either by convergence or by hitting the {{max_iterations}} cap), produce a closing summary:
- Iterations run.
- Net changes by kind (decisions revised, features proposed, etc.).
- Approaches synthesized with their `source_hash`.
- Any analyzer disagreements that landed as low-confidence revisions (so the operator can spot intent-vs-reality divergence).
- Convergence outcome: converged-cleanly OR hit-iteration-cap OR precondition-failed.
```

- [ ] **Step 2: Verify**

Run: `head -20 internal/scaffold/plans/code_assimilation.claude-code.md`
Expected: heading + readable structure.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/plans/code_assimilation.claude-code.md
git commit -m "feat(plans): code_assimilation Claude Code dynamic-workflow overlay (DJ-148)"
```

---

### Task 17: Author `internal/scaffold/plans/code_assimilation.interactive.md` (mode overlay)

**Files:**
- Create: `internal/scaffold/plans/code_assimilation.interactive.md`

The interactive overlay is for Codex/Gemini interactive mode; reuses the default playbook body but prepends a `## Loop control` block instructing the agent to drive iteration via `spec_loop_*` tools per DJ-142.

- [ ] **Step 1: Read the existing interactive overlay shape**

Run: `head -40 internal/scaffold/plans/spec_refinement.interactive.md`

Note the `## Loop control` directive header that DJ-142's spec_loop_* tools require.

- [ ] **Step 2: Create the file**

Write to `internal/scaffold/plans/code_assimilation.interactive.md`:

```markdown
# Code Assimilation (interactive — Codex / Gemini)

This overlay applies to interactive Codex and Gemini sessions for the `code_assimilation` activity. It wraps the default single-iteration playbook with the loop-state directive header so the agent self-drives iteration via the DJ-142 `spec_loop_*` MCP tools.

## Loop control

You are running this activity in **interactive mode**. The harness does NOT re-dispatch you between iterations — you drive iteration yourself via the daemon's loop-state tools:

- `mcp__locutus__spec_loop_begin` — call once at the start with `{activity: "code_assimilation", target: "<target-or-empty>"}`. Returns the iteration cap and the current iteration number (starts at 1).
- `mcp__locutus__spec_loop_status` — call to check whether you should continue. Returns `should_continue: bool` (false when at cap or marked converged).
- `mcp__locutus__spec_advance_iteration` — call between iterations to increment the counter and (optionally) record a per-iteration note for the report.

Do NOT emit a `converged:` verdict line for an outer harness — there is no harness in interactive mode; you are the harness. The loop terminates when (a) you've emitted `spec_loop_status` and it returned `should_continue: false`, or (b) you've decided no further work remains and called the natural end of the workflow.

The iteration cap is set by the activity registry per DJ-138; this activity's default is `max_iterations: 3`.

---

(Below this header, the default `code_assimilation.md` playbook body applies. Read it now and follow it for each iteration, calling `spec_advance_iteration` between iterations.)
```

- [ ] **Step 3: Verify**

Run: `head -10 internal/scaffold/plans/code_assimilation.interactive.md`
Expected: header + Loop control block.

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/plans/code_assimilation.interactive.md
git commit -m "feat(plans): code_assimilation interactive overlay with spec_loop_* directive (DJ-148)"
```

---

## Phase 7 — Documentation

### Task 18: Add Dry-Run paragraph in runtime-affordances + CLAUDE.md bullet + council.md update

**Files:**
- Modify: `docs/runtime-affordances.md` (add an assimilate paragraph after DJ-144 driver-matrix section)
- Modify: `CLAUDE.md` (add a DJ-148 bullet in "Sources of Truth")
- Modify: `docs/council.md` (mention assimilate playbook subagent flow alongside existing flows)

- [ ] **Step 1: Add `docs/runtime-affordances.md` paragraph**

Find the section that names DJ-144's driver matrix (search: `grep -n 'DJ-144' docs/runtime-affordances.md`). After that section, add:

```markdown
## Assimilate (DJ-148)

`locutus assimilate` reads brownfield source code, infers/revises features+decisions+strategies (code-is-truth direction), and synthesizes approaches binding inferred specs to source files with `source_hash`. Producing coherent (spec, code, approach) state is the verb's outcome per [DJ-148](decisions/dj-148-assimilate-bidirectional-reconciliation.md).

**Preconditions** (refused with a helpful error if missing): `GOALS.md` exists; goal layer is populated (operator runs `locutus refine goals` first). The operator's brownfield bootstrap workflow is `locutus init → edit GOALS.md → locutus refine goals → locutus assimilate → locutus adopt`, with each verb having one clear purpose.

The activity uses the same per-runtime convergence-driver pattern as `spec_refinement`: Claude Code drives the analyzer fan-out as a dynamic workflow (`code_assimilation.claude-code.md`); Codex/Gemini interactive self-loops via `spec_loop_*` (`code_assimilation.interactive.md`); Codex/Gemini headless uses the `OuterLoopRunner` (`code_assimilation.md` default fallback). `max_iterations` defaults to 3 per the registry (lower than `spec_refinement`'s 20 because assimilate is single-pass-shaped).

DJ-147 dry-run inherits automatically — the two new MCP tools (`spec_propose_approach`, `spec_revise_approach`) are wrapped via the same `captureOnly` registration-site adapter as the other 14 mutation tools.
```

- [ ] **Step 2: Add CLAUDE.md bullet**

In `CLAUDE.md`, find the "Sources of Truth" section. After the DJ-147 bullet, add:

```markdown
- **`assimilate` is bidirectional brownfield reconciliation (DJ-148).** Reads source code, infers/revises features+decisions+strategies (code-is-truth direction), synthesizes approaches binding them to source files with `source_hash`. Produces coherent (spec, code, approach) state — the maintenance loop's outcome. Preconditions: `GOALS.md` exists + goal layer populated (operator runs `refine goals` first); refuses with helpful errors otherwise. Reuses refine's `scout` + per-domain analyzers (`backend-analyzer` / `frontend-analyzer` / `infra-analyzer`) + `gap-analyst` subagents with prompts rewritten for current conventions (axis-shaped ids per DJ-133, no entity persistence per DJ-076). Adds `spec_propose_approach` / `spec_revise_approach` MCP tools + per-approach `source_hash` body field (shared with DJ-149). Per-runtime convergence drivers inherited from DJ-144. DJ-147 dry-run inheritance automatic. Defers middle-out reconciliation, orphan handling, self-contained goal-layer sync, and `code-paths` cache to DJ-150+ as future work. See [DJ-148](docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md).
```

- [ ] **Step 3: Update `docs/council.md`**

Find the section that lists the activity playbooks (search: `grep -n 'spec_refinement' docs/council.md`). Add a brief paragraph or subsection naming assimilate's subagent flow:

```markdown
**Assimilate (DJ-148)** — brownfield code → spec reconciliation. Subagent flow: scout surveys the codebase; backend/frontend/infra analyzers gather per-domain evidence in parallel; gap-analyst reconciles their contributions against the existing manifest with code-is-truth direction; orchestrator emits revisions + proposes via MCP tools and synthesizes approaches binding each inferred feature/strategy to its source files via `spec_propose_approach`. Convergence is single-pass-shaped; iteration only re-runs on transient failures.
```

- [ ] **Step 4: Verify bijection test**

Run: `go test ./internal/docs/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/runtime-affordances.md CLAUDE.md docs/council.md
git commit -m "docs(dj-148): document assimilate brownfield reconciliation"
```

---

## Phase 8 — Validation

### Task 19: Full suite + winplan e2e validation

- [ ] **Step 1: Full suite + vet + race**

Run: `go build ./... && go vet ./... && go test ./... -race 2>&1 | grep -vE '^ok|no test files' | tail -15`
Expected: empty output (everything passes under race).

- [ ] **Step 2: Rebuild binary**

```bash
go build -o locutus .
ls -la locutus
```

Verify binary built fresh.

- [ ] **Step 3: Stop any winplan daemon to pick up the new playbooks**

```bash
( cd /Users/chetan/projects/winplan && ./locutus mcp-stop || true )
```

- [ ] **Step 4: Hand off to operator for live winplan e2e (out of scope for this implementation; surface as a follow-up)**

The implementation tasks end at the full-suite + build + daemon-stop. End-to-end validation against winplan requires running a real coding-agent subprocess (Claude Code), which uses the operator's Anthropic subscription. Surface this as a pending operator task and report.

Suggested winplan validation commands the operator can run:

```bash
# 1. Confirm refusal when goal layer is empty (winplan should have a populated goal layer from prior runs; if not, this returns the precondition error)
( cd /Users/chetan/projects/winplan && ./locutus assimilate )

# 2. Dry-run an assimilate against winplan (no real mutations, captures preview)
( cd /Users/chetan/projects/winplan && ./locutus assimilate --dry-run )

# 3. JSON dry-run for jq inspection
( cd /Users/chetan/projects/winplan && ./locutus assimilate --dry-run --format json | jq '.captured[] | {tool, kind, id}' )
```

The first command will currently return the "no source files found" / "goal layer empty" / "code-is-truth" path depending on winplan's current state — note that winplan as currently constituted is a spec-only project (no source code), so assimilate will report nothing to assimilate. This is expected and validates Step 1's discovery + Step 0's precondition handling without actually mutating state.

For a more substantive e2e against a real codebase, the operator should target a project with both `GOALS.md` and source code present.

---

## Self-Review

- **Spec coverage:** DJ-148 §1 (verb scope) → Task 9 (registry + cmd). §2 (preconditions) → Task 15's Step 0. §3 (bidirectional reconciliation; code-is-truth) → Tasks 14 (gap-analyst), 15 (playbook Step 5+6). §4 (5-agent subagent pool with prompt rewrites) → Tasks 10–14. §5 (approach mutation tools) → Tasks 3-7. §6 (approach body schema extension) → Task 2. §7 (per-runtime convergence drivers; max_iterations=3) → Tasks 9, 15, 16, 17. §8 (single-pass + idempotency) → Task 15 Step 7. §9 (status default = inferred) → Tasks 14 (gap-analyst), 15 (playbook step 6). §10 (DJ-147 dry-run inheritance) → Tasks 5, 6, 7 (captureOnly wrapping). All ten decision points have implementing tasks.

- **Placeholder scan:** no TBD/TODO. Real code blocks in every code step. Real markdown content in every playbook authoring step. Real prompts in every agent rewrite (described as transformations of existing content with concrete substitutions, not "rewrite vaguely").

- **Type consistency:** `proposeApproachInput` / `reviseApproachInput` / `buildApproachBody` / `captureProposeApproach` / `captureReviseApproach` / `existingApproachCreatedAt` — all used consistently. `SourceFiles []string` / `SourceHash string` / `SourceHashSyncedAt time.Time` — used consistently across spec struct definition, builder, capture closures, and the playbook's hash-computation step. Description constants `descSpecProposeApproach` / `descSpecReviseApproach` named per existing convention.

- **Known calibrations at execution time:** Task 7's "TestDryRunCapturesReviseApproach" assumes the existing test file has a clear pattern for in-memory transport + dry-run setup — the implementer adapts to whatever shape `TestDryRunCapturesMutations` uses (table-driven vs. standalone). Task 15's "find <files> -type f | sort | xargs sha256sum | sha256sum" hash computation is the documented method; if a project's source set is too large for the shell pipe, the implementer can split into batches. Task 18's CLAUDE.md insertion point assumes "Sources of Truth" section persists in its current shape; if it's been reorganized, the implementer inserts in the equivalent location.
