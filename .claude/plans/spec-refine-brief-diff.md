# Plan: refine with focused brief + diff preview + history retention

## Context

Today `locutus refine <id>` exists per node kind (`RunRefine`, `RunRefineFeature`, `RunRefineStrategy`, `RunRefineBug`, `RunRefineApproach`). Each runs the relevant council slice and rewrites the node JSON / MD on disk.

Per CLAUDE.md every mutating verb supports `--dry-run`, but it's unclear what `--dry-run` actually outputs — likely the new node, not a diff. Three DX gaps make the refine loop friction-heavy:

1. **No focused brief.** "Refine `dec-postgres-aurora` because we should consider Cockroach for cross-region writes" has no surface; the council re-runs from scratch with the same context that produced the original. The model is left to re-derive what to change.

2. **No diff before persist.** Operator has no preview of what's about to change. A bad refine run silently overwrites a good decision.

3. **No retained history.** When refine rewrites `dec-foo.json`, there's no automatic capture of the prior version with the rationale for the change. `internal/history/` exists with an `Event` shape that fits this case, but the refine path doesn't write into it. Lineage is recoverable only via `git log` on the spec dir.

This plan adds three flags to refine: `--brief`, `--diff`, `--address-finding`. The history-write becomes automatic for every refine run. Together they turn refine from "regenerate the node" into "deliberately evolve the node."

Depends on the per-node renderer from `spec-snapshot.md` for diff display.

## Design

### CLI surface

```
locutus refine <id>                                    # current behavior, plus auto-history
locutus refine <id> --brief "..."                      # focused intent passed to council
locutus refine <id> --diff                             # apply, then print unified diff
locutus refine <id> --dry-run --diff                   # preview without writing
locutus refine <id> --address-finding <session>:<idx>  # brief sourced from a critic finding
locutus refine <id> --rollback                         # undo the last refine, restoring history.OldValue
```

`--brief` and `--address-finding` are mutually exclusive. `--rollback` is mutually exclusive with everything else.

### `--brief "..."` — focused intent

The brief becomes an additional system-side instruction prepended to the relevant agent's user message. Concretely:

For `RunRefine` (decision path): the cascade rewriter receives the existing decision + parent contexts + the brief as a "refinement intent" message:

```
## Refinement intent

Considering Cockroach for cross-region writes; revisit whether
Postgres on Aurora Serverless v2 still beats Cockroach for our
write-availability requirements during deployment cutover periods.
```

The agent's prompt receives this verbatim — the prompt body itself doesn't change; the brief is a per-call refinement directive. No new agent definition required.

For `RunRefineFeature`, `RunRefineStrategy`, `RunRefineBug`, `RunRefineApproach`: the same pattern — synthesizer / rewriter agent receives the brief as the "why we're refining" preamble.

For decision-path refines specifically, the brief also gets propagated through the cascade: when the rewriter rewrites linked features/strategies, they see the same brief so the rewrite stays consistent with the user's intent.

### `--address-finding <session>:<idx>` — sugar

Loads the finding text from `.locutus/sessions/<session>/calls/<critic>.yaml` at the given index in the critic's `issues[]` and uses it as the brief. The session reference follows the same shape as the critic-issue paths in the per-call YAML.

Validation: the session must exist; the critic call must exist; the index must be in range; the brief is built from the finding text plus a one-line preamble noting the source.

### `--diff` — unified diff display

Two modes:

1. With `--dry-run --diff`: compute the proposed node, render against the existing one, output the diff, write nothing.
2. With `--diff` alone: write the new node, then render the diff between the prior version (from `OldValue` in the recorded history event) and the new one.

Diff format is a unified diff over the **rendered** Markdown form of the node (from `internal/render/spec.go`), not the raw JSON. JSON-level diff is mechanically correct but full of noise from `updated_at` timestamps, `created_at`, alternatives reordering, etc. Markdown rendering normalizes those out and shows the human-meaningful change.

Implementation: render(prior) and render(new) → unified diff via the existing `internal/render` shape or a small `github.com/sergi/go-diff/diffmatchpatch`-style helper. Output framed as:

```
--- a/spec/decisions/dec-adopt-datadog-…  (before refine, history event evt-1234)
+++ b/spec/decisions/dec-adopt-datadog-…  (after refine, brief: "validate vendor-lock-in")
@@ Rationale @@
-[old prose]
+[new prose]

@@ Alternatives @@
+### Self-hosted Grafana Cloud (newly considered)
+...
```

Plus a brief summary footer:

```
---
Diff summary: rationale rewrite (3 paragraphs, 215 → 312 chars), 1 alternative added.
Spec hash: <old> → <new>.
```

### Auto-history — refine writes a structured event

Every refine run, regardless of flags, writes an event into `.borg/history/` via the existing `Historian.Record(event)`. Event shape:

```go
history.Event{
    ID:           "evt-refine-" + nodeID + "-" + timestamp,
    Timestamp:    now,
    Kind:         "spec_refined",
    TargetID:     nodeID,
    OldValue:     priorJSON,   // verbatim previous JSON content
    NewValue:     newJSON,     // verbatim new JSON content
    Rationale:    brief,        // empty string if no --brief was supplied
    Alternatives: nil,          // unused for refine events
}
```

The history layer already supports this — only the wiring needs adding. Each refine path (`RunRefine`, `RunRefineFeature`, etc.) reads the prior bytes, fires the LLM, writes the new bytes, then records the event.

This unblocks `--rollback` (next), `locutus history <id>` (already exists; surfaces refine events alongside other events), and the DJ-aware narrative reports `internal/history/narrative.go` already produces.

### `--rollback` — restore the prior version

Inverse of refine. Looks up the most recent `spec_refined` event for the given target ID, reads its `OldValue`, writes that back to the spec node JSON, and records a new `spec_rolled_back` event with `OldValue=current`, `NewValue=restored-prior`.

Does NOT cascade — rolling back a decision that was widely cascaded leaves the cascaded features/strategies referencing the new shape inconsistent with the rolled-back decision. Surface a warning: "rollback restored dec-X to v3; features feat-Y, feat-Z were rewritten in the same refine and remain at v4 — inspect with `locutus explain feat-Y --history` and rerun refine if needed."

### File layout

**New:**
- `internal/history/refine.go` — `RecordRefineEvent(h *Historian, nodeID, priorJSON, newJSON, brief string) error` and `LatestRefineEvent(h *Historian, nodeID string) (*Event, error)`.
- `internal/render/diff.go` — `RenderDiff(priorMD, newMD string) string` — unified-diff wrapper. (Either pull in `sergi/go-diff` or use a small in-tree implementation; the render is line-based so an in-tree LCS is ~80 LOC and avoids a dep.)
- `cmd/refine_brief.go` — flag parsing for `--brief`, `--address-finding`, `--diff`, `--rollback`. Or — simpler — extend `cmd/refine.go` directly. Pick one based on the existing file size; no strong preference.

**Modified:**
- `cmd/refine.go` — `RefineCmd` gains `Brief string`, `AddressFinding string`, `Diff bool`, `Rollback bool` fields. Each `RunRefine*` function takes an extra `RefineOptions` struct so the brief threads through to the agent input and the prior bytes are captured for history.
- `internal/agent/specgen.go` (or wherever the per-kind refine helpers live) — accept a `RefinementIntent string` extra field on the council prompt and prepend it to the relevant agent's user message.
- The per-kind agents that consume the brief (`spec_architect`, `synthesizer`, `rewriter`, `spec_reconciler`, `spec_finding_clusterer`) need their projection functions updated to splice `RefinementIntent` into their user prompt at a stable position. This is one-line-per-projection.

### Where the brief threads through

For decision refine (`RunRefine`):
1. Brief is captured on `RefineOptions.Brief`.
2. Cascade rewriter receives it as the first user-message line: `"Refinement intent: <brief>\n\n## Decision under refine\n…"`.
3. Linked feature/strategy rewrites in the same cascade run see the same brief verbatim — keeps the cascade coherent.

For feature/strategy/bug/approach refines:
1. Brief is captured on `RefineOptions.Brief`.
2. The synthesizer or rewriter agent receives it as the first user-message line.
3. No cascade by default — refining a feature doesn't auto-rewrite linked decisions; the user can do that explicitly.

### Test coverage

**Unit:**
- `cmd/refine_test.go` — flag-parsing matrix (mutual exclusion of `--brief` vs `--address-finding` vs `--rollback`).
- `internal/history/refine_test.go` — event round-trip, latest-event-by-target lookup.
- `internal/render/diff_test.go` — golden diffs for added paragraph, removed paragraph, restructured alternatives.

**Integration (with `MockExecutor`):**
- A scripted refine call with `--brief "..."` — assert the agent received the brief in its first user message.
- A scripted refine call with `--diff` — assert the diff output contains the expected `+` and `-` lines.
- `--dry-run --diff` — assert no file write occurred AND diff was printed.
- `--rollback` — script a refine event in history, then rollback, assert the spec file matches the OldValue.

**Live integration (env-gated):**
- Real `locutus refine dec-foo --brief "consider Cockroach"` against winplan — visual check that the new rationale references Cockroach.

### Sequencing within this plan

1. Auto-history first — purely additive, no flag surface, every refine run benefits immediately.
2. `--brief` second — needs the projection updates per agent. Small mechanical change.
3. `--diff` third — needs the per-node renderer from `spec-snapshot.md`. Builds on top of the diff helper.
4. `--rollback` fourth — needs auto-history landed.
5. `--address-finding` last — sugar; pure flag handling on top of `--brief`.

## Verification

- After `--brief` lands: `locutus refine dec-foo --brief "..." --dry-run` → console output shows the proposed change incorporates the brief language.
- After `--diff` lands: `locutus refine dec-foo --brief "..." --diff` → console output shows ONLY the changed sections, not the whole spec.
- After auto-history lands: `locutus history dec-foo` lists every refine event with its brief.
- After `--rollback` lands: refine then rollback returns the file to byte-identical pre-refine state.
- Round-trip: refine A → refine B → rollback → file matches post-A state.

## Out of scope

- Multi-step refine sessions (refine A, then refine B, then commit both). Each refine is its own atomic write today; that property holds.
- Refine across multiple nodes in one command (`locutus refine feat-foo strat-bar --brief "…"`). The user can scripts this with shell loops; in-tree multi-target adds workflow complexity for marginal gain.
- Conflict detection: if two parallel refines run on overlapping cascades, the second wins. Real risk only in CI / multi-user scenarios; out of scope today.
- Spec-hash awareness on refine: rejecting a refine because the spec moved underneath. Add when the use case appears; for now refine is a single-user-at-a-time op.
- Persisting the post-refine `JustificationBrief` from `spec-explain-justify.md` onto the decision JSON. Independent feature; mention here so the design doesn't preclude it (the JSON shape stays open to it).

## MCP exposure

The existing `refine` MCP tool (already registered in `cmd/mcp.go`)
gains the same flags as the CLI. One handler surface backs both.

**MCP tool input shape:**

```go
type refineInput struct {
    ID             string `json:"id"`
    Brief          string `json:"brief,omitempty"`
    AddressFinding string `json:"address_finding,omitempty"`  // "<session>:<idx>"
    Diff           bool   `json:"diff,omitempty"`
    DryRun         bool   `json:"dry_run,omitempty"`
    Rollback       bool   `json:"rollback,omitempty"`
}
```

Mutual-exclusion validation lives in the shared `RefineOptions`
construction so CLI and MCP enforce the same invariants.

**Result shape:**

- Default refine: text content with a one-line "refined dec-foo
  (session: …)" summary.
- `--diff` or `--dry-run --diff`: text content with the unified diff
  body, framed identically to the CLI output.
- `--rollback`: text content with the "rolled back dec-foo from <new>
  to <prior>" summary plus any cascade-stale warnings.

**Why MCP exposure matters here:** an agent assisting a developer can
respond to "the SRE critic flagged a vendor-lock-in concern on the
Datadog decision; address it" by issuing a single MCP call —
`refine({id: "dec-adopt-datadog-…", address_finding: "<sid>:<idx>"})`
— rather than asking the user to run a CLI command in another window
and report back. The agent stays inside its loop; the spec evolves
through the conversation.

**Mutating-tool guardrails:** `refine` is a write. The existing MCP
pattern (per the existing tool registration) returns a confirmation
text block; clients displaying tool results to the user see exactly
what was changed. Combined with the auto-history that records every
refine event, an erroneous agent-driven refine is recoverable via
`refine({id, rollback: true})`. The `--dry-run --diff` path is the
preview equivalent; agents can call it before the real refine when the
user wants confirmation.

Tool registration is a one-line update to the existing `refine` tool
in `cmd/mcp.go`; the handler shells out to the same `RunRefine*`
functions the CLI uses, with the new options struct threaded through.

## Sequencing relative to other plans

Depends on:
- `spec-snapshot.md` for the per-node Markdown renderer (consumed by `--diff`)

Doesn't depend on:
- `spec-explain-justify.md` (independent; either order works)

Natural pairing with `spec-explain-justify.md` because the adversarial-justify verdict suggests a refine command with a pre-populated brief. Once both ship, the loop is:

```
locutus justify dec-foo --against "..."   # output suggests:
locutus refine dec-foo --brief "..."      # apply the suggested brief
locutus status --full --diff <prev>       # verify against snapshot   (future)
```
