## DJ-104: `scout_brief` Is a First-Class Citation Kind

**Status:** shipped

**Decision:** Add `scout_brief` to the `Citation.Kind` enum alongside `goals`, `doc`, `best_practice`, `spec_node`. Both spec elaborators (`spec_feature_elaborator.md`, `spec_strategy_elaborator.md`) drop the legacy rule that forbade citing the scout brief — *"if a fact came from the scout brief … do not fabricate a citation kind for it — find a `best_practice` or `goals` anchor that justifies the same conclusion"* — and gain explicit guidance to cite `scout_brief` when a decision rests on a fact the (grounded) scout retrieved. The new kind requires `excerpt` verbatim, mirroring `goals` and `doc`, so grounded provenance survives the survey artifact being gone.

**Why:** the prior rule actively *severed* grounded provenance at the boundary between the scout (DJ-093, grounded) and the elaborators (ungrounded). When the scout's `technology_options` flagged a current major version or a vendor lifecycle shift, the elaborator was instructed to recast that grounded fact as a `best_practice` claim — which DJ-085 explicitly defines as a *"named precise best practice ('12-factor app: stateless processes' — not 'industry best practices')."* A version pin or vendor status is neither. The result was either (a) a vague best_practice citation that an architect_critic flag would catch under DJ-085 rule 6, or (b) the elaborator skipping the decision entirely. Either way, the system was discarding grounded provenance the scout had paid (in search-tool budget) to retrieve.

**Why this isn't a re-introduction of the failures DJ-090's follow-up warned about:** that warning targeted *aspirational fields in LLM output schemas* — fields downstream code didn't consume, which created shape pressure on weaker models to fill them with degenerate content (the named precedent was the Span citation removal). DJ-104 does not add a field. It adds an enum value to an existing field that elaborators already populate. The shape pressure on the model is unchanged: the same `Citations []Citation` slot is filled with one more allowed kind. Models that have nothing scout-brief-derived to cite continue to cite `goals` / `doc` / `best_practice` / `spec_node` exactly as before — the new kind is opt-in by the model's content, not mandated by structure.

**Why this doesn't trip DJ-093's reversal criterion (a):** DJ-093 reserved the right to revert grounding if scout output started "search-result-aggregation displacing engineering judgment," and was explicit that grounding is *"not a license to add output schema fields."* DJ-104 doesn't touch the scout's prompt, schema, or output shape — the scout brief is unchanged. What changes is downstream: an elaborator that *consumes* the brief is now allowed to cite it instead of laundering it. That puts more weight on the brief's existing fields (`technology_options`, `watch_outs`, `implicit_assumptions`) but doesn't ask the scout to produce more of them. Reversal criterion (a) was about scout *production* drift; this is consumption.

**The strict-form choice (excerpt required):** `scout_brief` joins `goals` and `doc` in the excerpt-required tier rather than `best_practice` and `spec_node` in the excerpt-optional tier. Reasoning: an excerpt-optional `scout_brief` re-opens the laundering loophole — a model could cite "scout_brief: technology_options" as bare reference and lose the grounded text it was supposed to preserve. The whole point of the kind is that the verbatim scout claim travels with the decision durably; making excerpt optional defeats that. The cost is one extra rule the elaborator must follow, which is cheap relative to the laundering it prevents.

**What lands:**

- `spec.Citation` doc comment lists `scout_brief` and notes the excerpt requirement. The struct is unchanged — `Kind` is a string, `Excerpt` already exists — so no schema migration is needed for prior decisions.
- Both elaborator scaffolds add the `scout_brief` row to the per-kind requirements list and replace the legacy "do not fabricate a citation kind" paragraph with positive guidance ("Prefer the most specific kind that fits…").
- Render (`internal/render/spec.go`) needs no change — citation rendering is generic over `Kind`.
- Reconciler (`internal/agent/reconcile.go`) needs no change — citations pass through untouched.
- `TestElaboratorPromptsAllowScoutBriefCitations` in `internal/scaffold/scaffold_test.go` locks in the prompt-level change against accidental regression.

**What stays the same:**

- The scout's prompt, output schema (`ScoutBrief`), and grounding posture (DJ-093). The brief is an unchanged input to elaborators.
- The other four citation kinds and their existing rules. `best_practice` still requires "named precise principle"; `goals` and `doc` still require excerpts.
- DJ-085's denormalization model — citations live on the decision; they do not become pointers.
- Existing decisions in `.borg/spec/` with `goals`/`doc`/`best_practice`/`spec_node` citations. Nothing about prior-authored content needs to change.

**Reversal criteria:** revert if (a) elaborators in real council runs cite `scout_brief` for facts that aren't actually scout-derived (laundering in the other direction — using `scout_brief` as a synonym for "I don't have a real citation"), at which point the architect_critic's rule 6 needs sharpening to validate the cited excerpt actually appears in the scout brief; or (b) the strict excerpt requirement turns out to dramatically reduce `scout_brief` use in practice (model finds it easier to skip than to copy the verbatim text), suggesting either the prompt needs more instruction or the requirement should soften. Neither is structural; both are tunable in the existing files.

**Reference:** extends DJ-085 (citation kinds), governed by DJ-090's follow-up (no aspirational fields) and DJ-093 (scout grounding's "shape unchanged" mandate). The architecture lesson — *grant grounding to discovery agents, propagate grounded provenance forward via citation rather than re-grounding every consumer* — is the same one that produced DJ-093 itself. DJ-104 closes the propagation step that DJ-093 left implicit.
