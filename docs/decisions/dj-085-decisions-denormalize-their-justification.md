## DJ-085: Decisions Denormalize Their Justification; Session Transcripts Are Debug-Only

**Status:** shipped

**Decision:** A council-generated `spec.Decision` carries its own justification record on the persisted node. New types `spec.Citation` and `spec.DecisionProvenance` are populated by the architect at proposal time and survive on disk under `.borg/spec/decisions/<id>.{json,md}`. Each citation is `{kind: "goals" | "doc" | "best_practice" | "spec_node", reference, span?, excerpt?}` with the verbatim excerpt persisted alongside the reference, so a citation survives the cited file moving or being rewritten. Every council-generated decision MUST carry at least one citation and a one-sentence `architect_rationale`; the architect critic flags violations.

**Why denormalize, not point at the session file:** session transcripts live under `.locutus/sessions/<date>/<time>/<sid>.yaml`, which is gitignored and explicitly ephemeral debug context. An earlier sketch of this feature stored a `SessionID` on each Decision so a future tool could load the full council exchange. That made the Decision's justification load-bearing on a file the user is encouraged to delete — exactly the wrong durability story for the spec graph, which is supposed to be the project's authoritative record.

The denormalized shape solves it cleanly: the citations + the architect's own reason are persisted on the spec node. The session file remains useful for full-fidelity debug (the verbatim prompts, the critic exchange, the revise round) but its absence costs nothing structural. The same posture as `models.yaml` (embedded source of truth + editable `.borg/` copy) and like git's commit object versus the working tree.

**What lands on each decision:**

- `Citations []Citation` — at least one entry. Each citation grounds the decision in something traceable: a span of GOALS.md, a doc the user imported, a named precise best practice ("12-factor app: stateless processes" — not "industry best practices"), or another spec node. Excerpts are persisted verbatim.
- `ArchitectRationale string` — one short sentence summary, distinct from the longer prose `Rationale` field. The audit-scan version of "why."
- `SourceSession string` — non-load-bearing pointer at the transcript file. Empty when the decision was not council-generated. The `justify` verb (when added) reads it as a hint; nothing breaks when the file is gone.
- `GeneratedAt time.Time` — stamped by `normalizeDecision` at persist time so future audits know how stale the provenance record is.

**What the architect's prompt requires:** every decision MUST emit at least one citation. Vague rationale without a citation is a critic flag (architect_critic rule 6). Best-practice citations must name something precise — vague appeals to "good engineering" don't satisfy the rule.

**Carve-outs:** decisions that did NOT come from the council (hand-authored by the user, inferred by `assimilate` from existing code, etc.) leave `Provenance` nil rather than carrying a hollow `Provenance{}`. Distinguishable from "council ran and returned nothing." Future `assimilate` work can populate Provenance with `kind: "spec_node"` self-references where appropriate, but the current path is to leave it empty.

**Reversal criteria:** DJ-085 reverses only if (a) we move sessions into source control (would make the pointer durable, but bloats the spec with multi-KB transcripts per refine — not on the table), or (b) the citation field set proves insufficient (would extend the schema, not abandon denormalization).

**Note for `justify` verb (forthcoming):** The verb reads `Provenance.Citations` directly to produce a defense report. When `SourceSession` resolves to an existing file, it can pull the full council exchange as supplementary context. When it doesn't, the durable Citations + ArchitectRationale + Alternatives + Rationale already in the spec are sufficient — the decision defends itself.
