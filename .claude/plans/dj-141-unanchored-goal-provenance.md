# DJ-141 Goal-Layer Provenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `anchored`/`unanchored` provenance distinction to the goal layer so inferred goals (implicit-from-mission, decision-crystallized) survive `GOALS.md` edits instead of being wiped, per [DJ-141](../../docs/decisions/dj-141-unanchored-goal-provenance.md).

**Architecture:** A goal/anti-goal is **anchored** when its `source_clause` is a verbatim `GOALS.md` excerpt and **unanchored** otherwise (carrying a free-text `origin` note instead). `source_clause` presence *is* the class — no enum, no boolean. The matcher's delete-by-absence rule fires only for anchored nodes; the matcher gains `promoted` (unanchored → anchored on a matching new clause) and `contradicted` (auto-resolve toward `GOALS.md`) moves. Bootstrap tags decision/mission-derived nodes honestly as unanchored. No migration (winplan is reset), no new node kinds, no new verbs.

**Tech Stack:** Go (stdlib `encoding/json`, `time`); MCP via `github.com/modelcontextprotocol/go-sdk`; testify; canonical agent prompts + activity playbooks as embedded markdown under `internal/scaffold/`.

---

## File Structure

- `internal/spec/types.go` — add `Origin` to `Goal` and `AntiGoal` structs.
- `internal/spec/origin_dj141_test.go` *(new)* — JSON round-trip + omitempty assertions for `Origin`.
- `internal/mcp/tools_spec_write.go` — add `origin` to goal/antigoal propose inputs; exactly-one-of validation in `buildGoalBody`/`buildAntiGoalBody`; drop `synced_at` from `updateGoalsMdHashInput` + always server-stamp; update tool `Description` constants.
- `internal/mcp/tools_spec_write_dj141_test.go` *(new)* — exactly-one-of enforcement; `Origin` round-trip through propose; server-stamped `synced_at`.
- `internal/scaffold/agents/spec-goal-diff-matcher.md` — provenance-tagged input; `promoted` + `contradicted` categories; delete-by-absence anchored-only; bootstrap mints unanchored with `origin`.
- `internal/scaffold/agents/spec_goal_diff_matcher_test.go` — widen the four-category test; add DJ-141 assertions.
- `internal/scaffold/plans/spec_refinement.md` — Step 0 applies `promoted`/`contradicted`; bootstrap step mints unanchored.
- `internal/scaffold/plans/spec_refinement_dj141_test.go` *(new)* — Step 0 DJ-141 guidance assertions.
- `internal/render/snapshot.go` — anchored/unanchored split counts + informational "inferred scope" section.
- `internal/render/snapshot_test.go` — assertions for the split + inferred-scope list.
- `internal/cli/explain*.go` (locate in Task 7) — show `origin` on unanchored goals.
- `CLAUDE.md` — goal-layer paragraph: add provenance.

---

## Task 1: `Origin` field on `Goal` and `AntiGoal`

**Files:**
- Modify: `internal/spec/types.go:283-290` (Goal), `internal/spec/types.go:305-314` (AntiGoal)
- Test: `internal/spec/origin_dj141_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/spec/origin_dj141_test.go`:

```go
// DJ-141 — Origin records provenance for unanchored goal-layer nodes
// (claims with no verbatim GOALS.md excerpt). Anchored nodes carry a
// non-empty SourceClause and omit Origin; unanchored nodes carry a
// non-empty Origin and an empty SourceClause.
package spec_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/spec"
)

func TestGoalOriginRoundTrips(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	g := spec.Goal{
		ID: "goal-win-number-computation", Title: "Win-number computation",
		Body: "Computes the vote target.", Origin: "dec-product-scope-boundary",
		CreatedAt: now, UpdatedAt: now,
	}
	raw, err := json.Marshal(g)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"origin":"dec-product-scope-boundary"`)

	var back spec.Goal
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "dec-product-scope-boundary", back.Origin)
	assert.Empty(t, back.SourceClause)
}

func TestGoalOriginOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(spec.Goal{ID: "goal-x", SourceClause: "verbatim clause"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "origin", "anchored node must omit empty origin")
}

func TestAntiGoalOriginRoundTrips(t *testing.T) {
	raw, err := json.Marshal(spec.AntiGoal{ID: "agoal-x", Origin: "mission statement"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"origin":"mission statement"`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run 'TestGoalOrigin|TestAntiGoalOrigin' -v`
Expected: FAIL — `unknown field 'Origin' in struct literal`.

- [ ] **Step 3: Add the field to both structs**

In `internal/spec/types.go`, in `Goal` (after `SourceClause`, line 287) add:

```go
	Origin       string    `json:"origin,omitempty" yaml:"origin,omitempty"`
```

In `AntiGoal` (after `SourceClause`, line 309) add the identical line. Update each struct's doc comment with one line: `// Origin records provenance for unanchored nodes (DJ-141): a free-text note ("mission statement", "dec-product-scope-boundary") set when SourceClause is empty. Exactly one of {SourceClause, Origin} is non-empty.`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run 'TestGoalOrigin|TestAntiGoalOrigin' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spec/types.go internal/spec/origin_dj141_test.go
git commit -m "feat(spec): add Origin field to Goal/AntiGoal for DJ-141 provenance"
```

---

## Task 2: `origin` param + exactly-one-of validation on propose/revise

**Files:**
- Modify: `internal/mcp/tools_spec_write.go:211-216` (proposeGoalInput), `:236-243` (proposeAntiGoalInput), `:704-717` (buildGoalBody), `:723-738` (buildAntiGoalBody)
- Test: `internal/mcp/tools_spec_write_dj141_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_spec_write_dj141_test.go`:

```go
// DJ-141 — propose/revise goal tools accept an optional `origin` and
// enforce exactly-one-of {source_clause, origin}: an anchored node
// supplies source_clause, an unanchored node supplies origin, never
// both and never neither.
package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGoalBodyExactlyOneOf(t *testing.T) {
	// neither → error
	_, err := buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b"}, timeZero())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of")

	// both → error
	_, err = buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", SourceClause: "c", Origin: "o"}, timeZero())
	require.Error(t, err)

	// source_clause only (anchored) → ok
	g, err := buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", SourceClause: "c"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "c", g.SourceClause)
	assert.Empty(t, g.Origin)

	// origin only (unanchored) → ok
	g, err = buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", Origin: "mission statement"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "mission statement", g.Origin)
	assert.Empty(t, g.SourceClause)
}

func TestBuildAntiGoalBodyExactlyOneOf(t *testing.T) {
	_, err := buildAntiGoalBody(proposeAntiGoalInput{ID: "agoal-x", Title: "X", Body: "b"}, timeZero())
	require.Error(t, err)
	a, err := buildAntiGoalBody(proposeAntiGoalInput{ID: "agoal-x", Title: "X", Body: "b", Origin: "dec-product-scope-boundary"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "dec-product-scope-boundary", a.Origin)
}
```

Note: `timeZero()` is a one-line helper — add to this test file: `func timeZero() time.Time { return time.Time{} }` and import `"time"`. (Package is `mcp`, not `mcp_test`, so the unexported `buildGoalBody` is reachable.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run 'TestBuild.*ExactlyOneOf' -v`
Expected: FAIL — `unknown field 'Origin'` then, once fields added, no error returned for neither/both.

- [ ] **Step 3: Add `origin` to the input structs**

In `proposeGoalInput` (line 215) change `SourceClause` jsonschema to mark it optional and add `Origin` after it:

```go
	SourceClause string `json:"source_clause,omitempty" jsonschema:"For an ANCHORED goal: verbatim text from GOALS.md that this goal interprets. Supply exactly one of source_clause or origin. Load-bearing for the diff-and-apply sync — preserves the goal's id across GOALS.md rephrasings by matching against this clause."`
	Origin       string `json:"origin,omitempty" jsonschema:"For an UNANCHORED goal (DJ-141): a short free-text note naming where the claim came from when it has NO verbatim GOALS.md excerpt — e.g. 'mission statement' or a scope-encoding decision id like 'dec-product-scope-boundary'. Supply exactly one of source_clause or origin. Unanchored goals are never deleted merely for being absent from GOALS.md."`
```

Add the identical `Origin` field (and the same `source_clause,omitempty` + jsonschema reword) to `proposeAntiGoalInput` after line 240.

- [ ] **Step 4: Add exactly-one-of validation + set Origin in the body builders**

In `buildGoalBody` (line 704), before assembling the struct:

```go
func buildGoalBody(in proposeGoalInput, createdAt time.Time) (spec.Goal, error) {
	if err := validateProvenance(in.SourceClause, in.Origin); err != nil {
		return spec.Goal{}, err
	}
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	return spec.Goal{
		ID:           in.ID,
		Title:        in.Title,
		Body:         in.Body,
		SourceClause: in.SourceClause,
		Origin:       in.Origin,
		CreatedAt:    createdAt,
		UpdatedAt:    now,
	}, nil
}
```

Apply the same `validateProvenance` guard and `Origin: in.Origin` line to `buildAntiGoalBody` (line 723). Add the shared helper near the body builders:

```go
// validateProvenance enforces DJ-141's exactly-one-of rule: a goal-
// layer node is either anchored (source_clause set, a verbatim GOALS.md
// excerpt) or unanchored (origin set, a provenance note for an inferred
// claim) — never both, never neither.
func validateProvenance(sourceClause, origin string) error {
	hasClause := strings.TrimSpace(sourceClause) != ""
	hasOrigin := strings.TrimSpace(origin) != ""
	switch {
	case hasClause && hasOrigin:
		return fmt.Errorf("supply exactly one of source_clause or origin, not both (an anchored node has source_clause; an unanchored node has origin)")
	case !hasClause && !hasOrigin:
		return fmt.Errorf("supply exactly one of source_clause or origin: set source_clause for a verbatim GOALS.md claim, or origin (e.g. 'mission statement') for an inferred claim")
	}
	return nil
}
```

(`strings` and `fmt` are already imported in this file.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mcp/ -run 'TestBuild.*ExactlyOneOf' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_dj141_test.go
git commit -m "feat(mcp): origin param + exactly-one-of provenance on goal propose/revise (DJ-141)"
```

---

## Task 3: Server-stamp `synced_at` (drop from tool input)

**Files:**
- Modify: `internal/mcp/tools_spec_write.go:271-274` (updateGoalsMdHashInput), `:584-601` (handler), `:78` (descSpecUpdateGoalsMdHash)
- Test: append to `internal/mcp/tools_spec_write_dj141_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/tools_spec_write_dj141_test.go`:

```go
func TestUpdateGoalsMdHashInputHasNoSyncedAtField(t *testing.T) {
	// DJ-141: synced_at is server-stamped, never agent-supplied. The
	// input struct must not carry a SyncedAt field (the agent has no
	// clock; on the winplan run it fabricated a midnight timestamp).
	var in updateGoalsMdHashInput
	in.Hash = "sha256:abc"
	// Compile-time guarantee via the assignment above; assert the JSON
	// shape carries only hash.
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "synced_at")
}
```

Add imports `encoding/json` to the test file if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestUpdateGoalsMdHashInputHasNoSyncedAtField -v`
Expected: FAIL — output contains `synced_at` (field still present).

- [ ] **Step 3: Drop the field + the parse branch**

Replace `updateGoalsMdHashInput` (lines 271-274) with:

```go
type updateGoalsMdHashInput struct {
	Hash string `json:"hash" jsonschema:"sha256:<hex> digest of the current GOALS.md bytes (use spec.ComputeGoalsMdHash to produce). Required; empty values are rejected. The sync timestamp is recorded server-side from the wall clock — there is no agent-supplied timestamp field."`
}
```

In the handler (lines 584-601) remove the `syncedAt` override branch so it reads:

```go
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateGoalsMdHashInput) (*mcp.CallToolResult, any, error) {
		hash := strings.TrimSpace(in.Hash)
		if hash == "" {
			return errorResult("spec_update_goals_md_hash: hash is required"), nil, nil
		}
		if err := store.UpdateGoalsMdHash(hash, time.Now().UTC()); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(fmt.Sprintf("Updated goals_md_hash to %s.", hash)), nil, nil
	})
```

- [ ] **Step 4: Update the tool description**

Edit `descSpecUpdateGoalsMdHash` (line 78): change `Input is {hash, synced_at} where ...` to `Input is {hash} where hash is the sha256:<hex> digest of the current GOALS.md bytes (use the canonical spec.ComputeGoalsMdHash helper to produce it). The sync timestamp is stamped server-side from the wall clock — the agent does not supply it.` Remove the `synced_at` sentence.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/mcp/ -run 'TestUpdateGoalsMdHash|TestBuild' -v`
Expected: PASS. Then `go build ./...` to confirm no caller passes `SyncedAt`.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_spec_write.go internal/mcp/tools_spec_write_dj141_test.go
git commit -m "fix(mcp): server-stamp goals_md synced_at, drop agent-supplied field (DJ-141)"
```

---

## Task 4: Update goal/antigoal tool `Description` constants for provenance

**Files:**
- Modify: `internal/mcp/tools_spec_write.go` — `descSpecProposeGoal`, `descSpecProposeAntiGoal`, `descSpecReviseGoal` (line 48), `descSpecReviseAntiGoal` (line 54)

Per CLAUDE.md, tool-behavior text lives in the registered `Description`, not the playbook. These need the anchored/unanchored semantics.

- [ ] **Step 1: Locate the propose descriptions**

Run: `grep -n 'descSpecProposeGoal\s*=\|descSpecProposeAntiGoal\s*=' internal/mcp/tools_spec_write.go`

- [ ] **Step 2: Edit `descSpecProposeGoal`**

Append to its text: ` A goal is ANCHORED when you supply source_clause (a verbatim GOALS.md excerpt) and UNANCHORED when you instead supply origin (a provenance note like 'mission statement' or a scope-encoding decision id) for a claim inferred without a verbatim GOALS.md sentence. Supply exactly one. Unanchored goals persist across GOALS.md edits and are never deleted merely for being absent from GOALS.md (DJ-141).`

- [ ] **Step 3: Edit `descSpecProposeAntiGoal`**

Append the same anchored/unanchored sentence, substituting "anti-goal" for "goal".

- [ ] **Step 4: Edit the revise descriptions (lines 48, 54)**

In `descSpecReviseGoal` and `descSpecReviseAntiGoal`, add: ` Revising an unanchored node to set source_clause (and clear origin) is the PROMOTED path — it anchors a previously-inferred node when a matching GOALS.md clause appears, preserving the id (DJ-141).`

- [ ] **Step 5: Verify build + existing tool tests**

Run: `go test ./internal/mcp/ -v 2>&1 | tail -20`
Expected: PASS (no behavioral change; descriptions are strings).

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_spec_write.go
git commit -m "docs(mcp): document anchored/unanchored provenance in goal tool descriptions (DJ-141)"
```

---

## Task 5: Rewrite the `spec-goal-diff-matcher` prompt

**Files:**
- Modify: `internal/scaffold/agents/spec-goal-diff-matcher.md`
- Test: `internal/scaffold/agents/spec_goal_diff_matcher_test.go` (widen) + DJ-141 assertions

**Before editing this prompt, walk `docs/agent-conventions.md` as a checklist** (anti-pattern priming, positive phrasing, hyphenated naming) — per the project's agent-conventions-first rule.

- [ ] **Step 1: Update the four-category test to six + add DJ-141 assertions**

In `spec_goal_diff_matcher_test.go`, find `TestSpecGoalDiffMatcherPromptDescribesFourDiffCategories` and extend its category slice to `{"unchanged", "modified", "deleted", "added", "promoted", "contradicted"}` and rename the test to `...DescribesSixDiffCategories`. Then add:

```go
// DJ-141 — the matcher must distinguish anchored (source_clause-backed)
// from unanchored (origin-backed) nodes, restrict delete-by-absence to
// anchored nodes, and describe the promoted/contradicted moves.
func TestSpecGoalDiffMatcherPromptDescribesProvenance(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	lower := strings.ToLower(body)
	for _, term := range []string{"anchored", "unanchored", "origin", "promoted", "contradicted"} {
		assert.Contains(t, lower, term, "matcher prompt must describe %q (DJ-141)", term)
	}
	assert.Contains(t, lower, "never deleted",
		"prompt must state unanchored nodes are never deleted by absence from GOALS.md")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/agents/ -run 'TestSpecGoalDiffMatcherPrompt(DescribesSix|DescribesProvenance)' -v`
Expected: FAIL — terms `anchored`/`promoted`/`contradicted` absent.

- [ ] **Step 3: Edit the prompt — provenance in Context**

In `spec-goal-diff-matcher.md`, in the "Context" section's "Existing goal-layer nodes" bullet (line ~26), append:

> Each node is tagged by **provenance**: an **anchored** node carries a non-empty `source_clause` (a verbatim `GOALS.md` excerpt); an **unanchored** node carries an empty `source_clause` and a non-empty `origin` note (e.g. `"mission statement"`, `"dec-product-scope-boundary"`) recording where an inferred claim came from when `GOALS.md` carries no sentence for it. Provenance decides whether a node is eligible for deletion-by-absence (below).

- [ ] **Step 4: Edit the prompt — gate `deleted`, add `promoted` and `contradicted`**

In the `## deleted` section (line ~53), prepend a mandate sentence:

> **Only anchored nodes are eligible for this category.** An unanchored node never claimed a `GOALS.md` clause, so its absence from `GOALS.md` is meaningless — it is **never deleted** by absence. An unanchored node leaves the graph only via the `contradicted` category (below) or an operator's explicit delete, never here.

Add two new sections after `## added`:

````markdown
## promoted

A current `GOALS.md` claim semantically matches an existing **unanchored** node's `body` at the same polarity. The operator has stated, in `GOALS.md`, a claim the goal layer was already carrying as an inferred (origin-backed) node. This is a promotion, not a new node: the id is preserved and the node becomes anchored. Emit:

- `id` — the existing unanchored node's id, copied verbatim.
- `new_source_clause` — the verbatim `GOALS.md` excerpt the node now anchors to. The orchestrator sets this and clears `origin`, flipping the node to anchored.
- `new_body` — the interpretation prose, refreshed against the now-explicit clause when the wording sharpens it; otherwise the existing body.

Match by semantic overlap between the new clause and the unanchored node's `body` (these nodes have no `source_clause` to match against). Prefer promotion over `added` whenever an unanchored node plainly covers the new claim — minting a new node would duplicate the claim and orphan the unanchored node.

## contradicted

A current `GOALS.md` claim asserts the **opposite polarity** of an existing node — an in-scope assertion against an `agoal-*` that cedes it, or an out-of-scope carve-out against a `goal-*`. Polarity lives in the node type, so the id cannot survive the flip. `GOALS.md` is canonical, so the contradicting claim wins. Emit:

- `retire_id` — the existing opposite-polarity node to delete.
- `new_node` — the full `added`-shape payload for the new-polarity node the claim now establishes (anchored: it has a verbatim `source_clause`).
- `citing_ids` — the ids of every `dec-*` / `feat-*` / `strat-*` / `app-*` node whose `.advances` / `.respects` currently cites `retire_id`. The orchestrator surfaces these for content review, because deleting the node drops their citation but does not fix their content.

Use `contradicted` only for genuine polarity conflict. A claim that merely refines or agrees with an existing node is `modified`/`promoted`, not `contradicted`.
````

- [ ] **Step 5: Edit the prompt — bootstrap mints unanchored**

In the bootstrap hint paragraph (line ~27), replace the existing text with:

> **Optional bootstrap hint** — on the one-time first run for a project that already carries scope-encoding decisions (e.g. `dec-product-scope-boundary`), the user message names them as secondary sources and instructs first-run bootstrap. Treat their bodies — and the implicit scope in the `GOALS.md` mission statement — as claim sources. Claims sourced this way have **no verbatim `GOALS.md` excerpt**, so emit them as **unanchored** `added` entries: omit `source_clause` and set `origin` to the backing decision id (e.g. `"dec-product-scope-boundary"`) or `"mission statement"`. Only claims with genuine verbatim In/Out-of-Scope text in `GOALS.md` are emitted anchored. Never back-form a `source_clause` from a decision body — that fabricates provenance the next sync would then fail to match. On subsequent runs the goal layer is non-empty and `GOALS.md` is the sole source.

In the `## added` section's field list (line ~69), change `source_clause` to: `source_clause` *(anchored only)* — verbatim excerpt from current `GOALS.md`; **or** `origin` *(unanchored only)* — a provenance note when the claim has no `GOALS.md` sentence. Emit exactly one.

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/scaffold/agents/ -v 2>&1 | tail -20`
Expected: PASS (including the widened six-category test and the new provenance test).

- [ ] **Step 7: Commit**

```bash
git add internal/scaffold/agents/spec-goal-diff-matcher.md internal/scaffold/agents/spec_goal_diff_matcher_test.go
git commit -m "feat(agents): matcher gains anchored/unanchored provenance + promoted/contradicted moves (DJ-141)"
```

---

## Task 6: Update the `spec_refinement` playbook Step 0

**Files:**
- Modify: `internal/scaffold/plans/spec_refinement.md` (Step 0, lines ~60-77; bootstrap step ~70)
- Test: `internal/scaffold/plans/spec_refinement_dj141_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/scaffold/plans/spec_refinement_dj141_test.go`:

```go
// DJ-141 — Step 0 applies the matcher's new categories: `promoted`
// (revise the unanchored node to set source_clause + clear origin),
// `contradicted` (delete the opposite-polarity node, propose the new
// one, surface citing nodes as at-risk), and mints bootstrap nodes as
// unanchored with origin.
package plans_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpecRefinementPlaybookDescribesProvenanceMoves(t *testing.T) {
	body := strings.ToLower(loadSpecRefinement(t))
	for _, term := range []string{"promoted", "contradicted", "unanchored", "origin"} {
		assert.Contains(t, body, term, "Step 0 must describe %q (DJ-141)", term)
	}
}

func TestSpecRefinementPlaybookBootstrapMintsUnanchored(t *testing.T) {
	body := strings.ToLower(loadSpecRefinement(t))
	assert.Contains(t, body, "unanchored",
		"bootstrap step must mint decision/mission-derived nodes as unanchored")
	assert.Contains(t, body, "never fabricate",
		"bootstrap step must forbid fabricating a source_clause from decision bodies")
}
```

(`loadSpecRefinement` already exists in `spec_refinement_dj139_test.go`, same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/plans/ -run 'TestSpecRefinementPlaybook(DescribesProvenanceMoves|BootstrapMintsUnanchored)' -v`
Expected: FAIL — terms absent.

- [ ] **Step 3: Edit the apply-the-diff step**

In `spec_refinement.md` Step 0, in the diff-application step (line ~73, where `modified`/`deleted`/`added` are mapped to tool calls), add two bullets:

```markdown
   - `promoted` (`goal-*` / `agoal-*`) — the matcher matched a current GOALS.md clause to an existing **unanchored** node. Call `mcp__locutus__spec_revise_goal` / `spec_revise_antigoal` with the node's `id`, the matcher's `new_source_clause` as `source_clause`, and **no** `origin` — this anchors the previously-inferred node in place, preserving the id and its incoming citations.
   - `contradicted` — the matcher found a GOALS.md claim of opposite polarity to an existing node. GOALS.md is canonical, so auto-resolve: call `spec_delete_goal` / `spec_delete_antigoal` on the matcher's `retire_id` (the reason names the GOALS.md edit), then `spec_propose_goal` / `spec_propose_antigoal` for the matcher's `new_node`. Record the matcher's `citing_ids` — every node whose citation pointed at `retire_id` — and surface them in your Step 3 / final report as at-risk: their `.advances`/`.respects` lost a target and their **content may now be mis-scoped** (the citation walk re-anchors metadata, not feature bodies).
```

In the `deleted` bullet, add: ` Only anchored nodes appear here; unanchored nodes are never deleted by absence (DJ-141).`

- [ ] **Step 4: Edit the bootstrap step (line ~70)**

Append to the first-run bootstrap affordance:

> Claims sourced from a decision body or the implicit mission statement have no verbatim `GOALS.md` excerpt — instruct the matcher to emit them as **unanchored** (`origin` set to the decision id or `"mission statement"`, `source_clause` omitted), and apply them via `spec_propose_goal` / `spec_propose_antigoal` with `origin`. **Never fabricate** a `source_clause` from a decision body: an unanchored node persists across `GOALS.md` edits, whereas a fake-anchored node would be deleted on the next sync when its invented clause isn't found in `GOALS.md`.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/scaffold/plans/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/plans/spec_refinement.md internal/scaffold/plans/spec_refinement_dj141_test.go
git commit -m "feat(plans): Step 0 applies promoted/contradicted + unanchored bootstrap (DJ-141)"
```

---

## Task 7: Render anchored/unanchored split + `explain` origin

**Files:**
- Modify: `internal/render/snapshot.go:27-28` (count fields), `:205-207` (population), `:499-505` (rendering)
- Test: `internal/render/snapshot_test.go`
- Modify (Step 5): the `explain` renderer (locate via grep)

- [ ] **Step 1: Write the failing test**

In `internal/render/snapshot_test.go`, add a test that builds a `Loaded` with two goals (one `SourceClause`-set, one `Origin`-set) and one anti-goal (`Origin`-set), renders the full snapshot, and asserts:

```go
func TestSnapshotSplitsAnchoredUnanchored(t *testing.T) {
	// Build a Loaded with 1 anchored goal, 1 unanchored goal, 1 unanchored agoal.
	// (Use the existing test-helper constructor pattern in this file.)
	out := renderFullSnapshotMarkdown(t, /* loaded */) // match existing helper name
	assert.Contains(t, out, "Anchored:")
	assert.Contains(t, out, "Unanchored:")
	assert.Contains(t, out, "inferred scope not yet stated in GOALS.md")
}
```

Adjust the helper/constructor calls to match the existing patterns in `snapshot_test.go` (read the file's existing `Loaded` fixtures first; reuse them).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -run TestSnapshotSplitsAnchoredUnanchored -v`
Expected: FAIL — strings absent.

- [ ] **Step 3: Add split counts to the snapshot data**

In `snapshot.go` near lines 27-28 add:

```go
	AnchoredGoalCount   int `json:"anchored_goal_count"`
	UnanchoredGoalCount int `json:"unanchored_goal_count"`
	// UnanchoredGoals lists "<id> — <title> (<origin>)" for the
	// informational "inferred scope" section (DJ-141).
	UnanchoredGoals []string `json:"unanchored_goals,omitempty"`
```

In the population block (lines ~205-207) compute them from `l.Goals` and `l.AntiGoals` (anchored ⇔ `Spec.SourceClause != ""`):

```go
	for _, g := range l.Goals {
		if strings.TrimSpace(g.Spec.SourceClause) != "" {
			data.AnchoredGoalCount++
		} else {
			data.UnanchoredGoalCount++
			data.UnanchoredGoals = append(data.UnanchoredGoals,
				fmt.Sprintf("%s — %s (%s)", g.Spec.ID, g.Spec.Title, g.Spec.Origin))
		}
	}
	sort.Strings(data.UnanchoredGoals)
```

(Apply the same anchored/unanchored tally to `l.AntiGoals` if you want a symmetric agoal split; per DJ-141 §9 the headline split is on goals — keep agoals counted in `AntiGoalCount` only unless the snapshot test demands otherwise.)

- [ ] **Step 4: Render the split + inferred-scope section**

In the `## Goal layer` block (lines ~499-505) change the count line to:

```go
		fmt.Fprintf(&b, "**Goals:** %d (Anchored: %d · Unanchored: %d) · **Anti-goals:** %d\n\n",
			d.GoalCount, d.AnchoredGoalCount, d.UnanchoredGoalCount, d.AntiGoalCount)
		if len(d.UnanchoredGoals) > 0 {
			b.WriteString("_Inferred scope not yet stated in GOALS.md — candidates to make explicit:_\n\n")
			for _, g := range d.UnanchoredGoals {
				fmt.Fprintf(&b, "- %s\n", g)
			}
			b.WriteString("\n")
		}
```

- [ ] **Step 5: Show `origin` in `explain`**

Run: `grep -rn -iE 'source_clause|SourceClause|func.*[Ee]xplain' internal/ cmd/ | grep -i explain` to locate the explain renderer for goal nodes. In the goal/anti-goal branch, where `SourceClause` is printed, add: when `SourceClause == ""` and `Origin != ""`, print `Origin (unanchored): <origin>` instead of an empty source-clause line. Add a focused test in the same package asserting an unanchored goal's explain output contains `unanchored` and the origin text.

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/render/ ./cmd/ -v 2>&1 | tail -25`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/render/snapshot.go internal/render/snapshot_test.go internal/cli # adjust paths to what Step 5 touched
git commit -m "feat(render): anchored/unanchored goal split + inferred-scope surface + explain origin (DJ-141)"
```

---

## Task 8: Update CLAUDE.md goal-layer paragraph

**Files:**
- Modify: `CLAUDE.md` (the DJ-139 goal-layer bullet under "Sources of Truth")

- [ ] **Step 1: Edit the goal-layer paragraph**

In the `**Goal layer persists the LLM's interpretation of GOALS.md (DJ-139).**` bullet, append a sentence:

> Per [DJ-141](docs/decisions/dj-141-unanchored-goal-provenance.md), each goal/anti-goal is **anchored** (non-empty `source_clause`, a verbatim `GOALS.md` excerpt) or **unanchored** (empty `source_clause`, a free-text `origin` note for a claim inferred from the mission statement or crystallized into a decision). The matcher deletes anchored nodes when their clause leaves `GOALS.md` but never deletes unanchored nodes by absence; a `GOALS.md` edit that states an inferred claim **promotes** the unanchored node in place, and an opposite-polarity edit **auto-resolves** toward `GOALS.md` (delete + re-propose, citing nodes surfaced as at-risk).

- [ ] **Step 2: Verify the decisions-manifest test still passes**

Run: `go test ./internal/docs/ -v`
Expected: PASS (DJ-141 file ↔ manifest row bijection holds — added in the design commit).

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(CLAUDE.md): document anchored/unanchored goal provenance (DJ-141)"
```

---

## Task 9: Full suite + end-to-end validation against reset winplan

**Files:** none (validation only)

- [ ] **Step 1: Full suite + vet + race**

Run: `go build ./... && go vet ./... && go test ./... -race 2>&1 | tail -30`
Expected: all PASS.

- [ ] **Step 2: Bootstrap the reset winplan**

With winplan reset (GOALS.md + decisions + features + strategies present, no `goal-*`/`agoal-*`), run `locutus refine goals` from the winplan dir. Then inspect `.borg/spec/goals/` + `.borg/spec/antigoals/`:

Run: `for f in /Users/chetan/projects/winplan/.borg/spec/goals/*.json; do python3 -c "import json; d=json.load(open('$f')); print(d['id'], 'ANCHORED' if d.get('source_clause') else 'unanchored:'+d.get('origin','MISSING'))"; done`
Expected: decision/mission-derived nodes are `unanchored:<origin>`; no node has a fabricated `source_clause`. The manifest's `goals_md_synced_at` is the real run time, not midnight.

- [ ] **Step 3: Exercise promote + contradict**

Edit winplan `GOALS.md` to add an in-scope clause matching an existing unanchored goal (expect **promote**: same id, now anchored) and an in-scope clause contradicting an existing `agoal-*` (expect **contradict**: agoal deleted, goal minted, citing features surfaced as at-risk). Re-run `locutus refine goals`. Confirm via `git diff .borg/spec/` that no unanchored goal was deleted merely for being absent from GOALS.md, and citations on untouched nodes are intact.

- [ ] **Step 4: Report**

Summarize: bootstrap provenance correct (no fabrication), promote/contradict behaved, no spurious deletions. This is the DJ-141 validation deferred from the DJ-139 doc.

---

## Self-Review Notes

- **Spec coverage:** DJ-141 §1-2 → Tasks 1,2; §3 (delete-gate) → Task 5; §4 (matcher tree) → Task 5; §5 (promoted) → Tasks 4,5,6; §6 (contradicted) → Tasks 5,6; §7 (bootstrap) → Tasks 5,6; §8 (no migration) → nothing to build (Task 9 validates the reset path); §9 (visibility) → Task 7; §10 (synced_at) → Task 3. All covered.
- **Type consistency:** `Origin` field name used identically across Tasks 1,2,5,7; `validateProvenance` defined once (Task 2); `proposeGoalInput`/`proposeAntiGoalInput` field additions match the build-fn reads.
- **Known soft spots to resolve at execution time:** Task 7 Step 1/5 reference existing helper names in `snapshot_test.go` and the `explain` renderer location — read those files first and match their actual fixtures/signatures rather than assuming the names above.
