## DJ-072: CLI Surface Consolidated to 8-Verb Lifecycle Shape

**Status:** shipped

**Decision:** Replace the 13 enumerative commands that accumulated through Tiers 1–8 (plus post-Tier-8 streaming work) with an 8-verb lifecycle that maps one-to-one to the phases a user actually moves through when operating on a Locutus project.

**The 8 verbs:**

| # | Command | Lifecycle phase | Purpose |
| --- | --- | --- | --- |
| 1 | `locutus init` | Bootstrap | Create the `.borg/` scaffold. |
| 2 | `locutus update` | Bootstrap | Refresh the binary and embedded defaults. |
| 3 | `locutus import <source>` | Admit | Bring a new feature/bug into the spec, gated by GOALS.md triage. |
| 4 | `locutus refine <node>` | Deliberate | Council-driven deliberation on any spec node (Goal, Feature, Strategy, Decision, Approach, Bug). |
| 5 | `locutus assimilate` | Infer | Infer or update the spec from an existing codebase (was `analyze`). |
| 6 | `locutus adopt` | Execute | Run the DJ-068 reconcile loop — bring code into alignment with the current spec. |
| 7 | `locutus status` | Observe | Snapshot of current state, drift, and validation errors. |
| 8 | `locutus history` | Recall | Query the historian's past-tense record (events, alternatives, narrative). |

**`--version` is a global flag** rather than a subcommand. `locutus mcp` remains the transport entry point for MCP clients; `locutus mcp-perm-bridge` remains a hidden internal subprocess.

**Deleted from the surface:** `version` (→ flag), `check` (→ gated inside `adopt`), `diff` (→ `refine --dry-run`), `regen` (→ `adopt`), `revisit` (→ `refine`, widened to all node kinds), `triage` (→ `import` admission gate), `analyze` (renamed to `assimilate`).

**Principle: one verb per lifecycle phase.** The old surface was organised around implementation deliverables (one subcommand per tier). The new surface is organised around user intent. A user never wants to "diff" in isolation — they want to preview the impact of a refinement, which is `refine --dry-run`. A user never wants to "triage" in isolation — they want to import something, and triage is the gate the admission passes through. Collapsing the verbs exposes the actual workflow.

**Every mutating verb supports `--dry-run`.** Consistent across `import`, `refine`, `assimilate`, `adopt`. Read-only verbs (`status`, `history`) do not. The dry-run implementation uses a `readOnlyFS` wrapper ([cmd/readonly_fs.go](../cmd/readonly_fs.go)) that drops writes while still serving reads — the pipeline runs to completion and the report reflects what would have been written.

**Why consolidate now rather than after more features land:**
The 13-command surface had already begun to produce rot: stubbed CLI handlers (`check`, `import` CLI), "LLM not configured" errors on four commands even though Genkit was wired end-to-end, duplicated logic between the CLI and MCP tool registrations, and a stale `docs/IMPLEMENTATION_PLAN.md` pointer in CLAUDE.md. Every new feature had to pick between fitting into the old shape or breaking from it. Consolidating before the reconciler matures means the reconciler is built against the final shape, not reworked into it later.

**Alternatives considered:**

- **Leave the 13 commands, add `adopt` alongside.** Rejected. The old commands weren't all real — four were LLM-blocked stubs, two were pure aliases-in-waiting. Shipping `adopt` into a crowded surface where half the verbs don't work would have been misleading to users and confusing to the next Claude session.
- **Keep compatibility aliases indefinitely.** Considered and rejected. Aliases were used during the multi-phase transition but deleted in Phase D the same session the consolidation landed. The project has exactly one user (the author); leaving aliases in place would have produced rot rather than reduced risk.
- **Expose `diff` and `check` as top-level verbs alongside the 8-verb set.** Rejected. `diff` is a dry-run of `refine` — its blast-radius output is exactly the cascade preview that `refine <id> --dry-run` emits. `check` is a pre-condition gate that only matters when `adopt` is about to dispatch — surfacing it standalone added a concept that didn't correspond to a user decision.

**Implementation in four phases (all landed 2026-04-21):**

- **Phase A — wiring + renames + history.** Real Genkit LLM construction via `cmd/llm.go::getLLM()` (lazy, env-var-driven); `analyze` → `assimilate`, `revisit` → `refine` (generalised to dispatch on node kind via `resolveNodeKind`); new `history` command with narrative/alternatives/events queries; `--version` flag replaces the `version` subcommand.
- **Phase B — fold-ins + dry-run.** `triage` folded into `import` as the admission gate; `diff` folded into `refine --dry-run`; `--dry-run` added on all mutating verbs; `check.CheckPrereqs` factored so it can be consumed by both the CLI and `adopt` without duplication.
- **Phase C — minimum viable `adopt` (DJ-068 partial implementation).** `spec.ComputeSpecHash` / `ComputeArtifactHashes` (with a `ReadFunc` indirection to avoid a spec→specio import cycle); `internal/reconcile` package implementing the five DJ-068 classification branches (`unplanned`, `drifted`, `out_of_spec`, `live`, preserved-prior-terminal); `SpecGraph.ApproachesUnder` for scope filtering; `adopt` command that classifies, runs the prereq gate, persists `planned` status, and surfaces `out_of_spec` drift with non-zero exit codes.
- **Phase D — cleanup.** Aliases removed, legacy commands deleted, duplicate MCP tool registrations dropped, `CLAUDE.md` rewritten to drop the stale `IMPLEMENTATION_PLAN.md` pointer and name the canonical surface.

**Deferred from Phase C (next rounds):**

- **Cascade write-back (DJ-069).** When a Decision is revised, parent Feature/Strategy present-tense statements need to be rewritten and child Approaches marked `drifted`. Today the classifier detects the drift via `spec_hash` diff, but no code actually rewrites parents or mutates status — that requires a dedicated rewriter agent prompt and remains TODO.
- **Pre-flight clarification (DJ-071).** The `pre_flight` enum value exists and the status machine recognises it, but the clarify-only agent call, ambiguity resolution, and `assumed`-Decision creation are not wired. `adopt` currently transitions drifted Approaches directly from `drifted` → `planned`.
- **Actual agent dispatch inside `adopt`.** Phase C wires classification and plan generation but stops short of invoking the dispatcher. The dispatcher package is fully built; connecting it to `adopt`'s planned workstreams is the next round of work.

The current `adopt` is honest about this scope — it classifies, gates on prereqs, persists planning intent, and reports what would happen. It does not claim to reconcile end-to-end yet. Future DJs (or a follow-up DJ-073/074) will cover the remaining rounds of Phase C.

**Phase plans live at `.claude/plans/verb-set-phase-{a,b,c,d}.md`** for reference on the detailed implementation choices in each phase.

**Impact on DJ-068 and DJ-069:**
Both DJs remain authoritative on the design of the state store, node types, cascade, and pre-flight. Phase C's minimum viable `adopt` is the first implementation increment against them — it validates the schema and classification model but leaves the cascade/pre-flight mechanics for later. Neither DJ is superseded.
