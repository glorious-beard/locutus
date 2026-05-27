## DJ-076: Entity Is In-Memory Context, Not a Persisted Spec Node

**Status:** shipped

**Decision:** `spec.Entity` (plus `EntityField` and `Relationship`) remains a live type in the codebase, populated by the assimilation pipeline and attached to `AssimilationResult.Entities`. Downstream agents (planner, supervisor, remediator) receive it as structured context so they can reason about existing data models without re-parsing Go/proto/SQL on every call. But Entities are **never persisted** — there is no `.borg/spec/entities/` directory, no `Entity` marshals to disk, and `loadExistingSpec` does not populate `ExistingSpec.Entities` from storage. A fresh assimilate run reconstructs the projection from code. The `KindEntity` enum value is retained so agent JSON output can be tagged as entity-kind for routing, but no graph edge, cascade path, or lifecycle treats entities as first-class spec nodes.

**Why this distinction matters:**

The confusion that surfaced during Round 1 was treating "formal extraction of a domain model" as equivalent to "persisted spec node." They're separate concerns. The extraction is valuable — an LLM planning a new Feature against an existing codebase benefits from seeing `User{id, email, password_hash} has_many Order` rather than being asked to infer the data model from scratch every call. That value is real at *inference time*, in one run. The value of *persisting* that structure across runs is much weaker: the code is the authoritative schema; a serialized entity file is a cache that drifts the moment someone renames a field.

In short: formal extraction earns its keep as agent context; formal persistence doesn't, because the code never stopped being the source of truth.

**What remains in code:**

- `spec.Entity`, `spec.EntityField`, `spec.Relationship` — structured types for passing between agents.
- `spec.KindEntity` — enum value (marked with a comment noting the DJ-076 semantics) so typed output from analyzer agents can be routed.
- `AssimilationResult.Entities` — populated by the pipeline; consumed in the same process.
- `ExistingSpec.Entities` — optional field the caller can populate when re-running against a previous projection, explicitly nullable.
- `backend_analyzer.md` prompt — still extracts entities; the output just doesn't get persisted.

**What's deliberately NOT there:**

- `.borg/spec/entities/` directory.
- Entity handling in `persistAssimilationResult` (the persistence loop skips entities with a code comment pointing at this DJ).
- Graph edges involving `KindEntity` nodes (`SpecGraph.BuildGraph` never adds them).
- Cascade or reconcile logic for entities.

**Why code, not spec, is authoritative for entity structure:**

1. **Correctness.** `grep 'type User struct'` is always right. A persisted entity file is right only until the next code change; it's a cache with unavoidable drift.
2. **Coding agents can read code.** That's the premise. If an agent needs to know User's fields, it opens the file. What an agent *can't* read from code is intent — "why bcrypt" or "what this feature does for users" — and that goes in Feature/Decision/Strategy prose, where it already belongs.
3. **Lifecycle has no obvious meaning.** Features have `proposed → active → removed`; Decisions have `proposed → assumed → inferred → active`; both map to user-visible judgements. What's `status: proposed` for a User struct that already exists in code? The lifecycle concept doesn't transfer.
4. **Drift detection doesn't need an Entity abstraction.** Feature/Approach hash-drift already captures "this file changed underneath the spec node that governs it." That mechanism catches field renames by surfacing the Approach that owned the file, which is more useful than abstract "entity changed."

**Alternatives considered and rejected:**

- **Delete Entity entirely.** Considered during the same conversation. Rejected because the in-memory extraction has real value as agent context — dropping the type would lose that. The mistake was conflating "keep the type" with "keep the persistence."
- **Persist to `.borg/spec/domain.md` as a flat doc.** Rejected: even a single doc is a cache that drifts against code, with all the same problems as per-entity files. If the value is agent context, the LLM can reconstruct it fresh on each assimilate run in less time than it takes to decide whether the on-disk doc is current.
- **Persist entities with `status: inferred` and cascade on field rename.** Rejected as overkill. The cascade model exists to keep spec prose consistent with Decisions; it doesn't buy anything when the "spec" in question is already machine-generated from code.

**Impact on adjacent DJs:**

- **DJ-069 (shipped):** node redesign DAG is unchanged. Entities were already not in `Goal → Feature/Strategy → Decision/Approach`; DJ-076 makes that exclusion explicit and rationalised.
- **DJ-075 (shipped):** updated in-place — "Entity persistence" removed from the "not in scope" deferrals list; DJ-076 is the resolution.
- **Round 5 of the gap-closeout plan:** the remediator uses in-memory entities to frame gap descriptions ("no tests for User"), still works exactly as intended.
- **Original IMPLEMENTATION_PLAN.md Tier 6e** (deleted): `EntityExtractor` framework-specific implementations (Go structs, proto, TypeScript, SQL migrations) remain a future extension — the *extraction* has room to grow even though the *persistence* is frozen as "none."

**Invariant to preserve going forward:** when future work wants to give Entity behaviour that feels lifecycle-adjacent (cascade, refine, etc.), first ask whether the same behaviour attached to the Feature/Strategy/Decision that *uses* the entity would be cleaner. In every case examined so far, it was.

**Forward-looking note — the extraction itself is provisional.** The persistence decision ("never write entities to `.borg/spec/`") is firm, because the argument against it is purely mechanical (the code is the source of truth; a serialised file is always a drifting cache). The *extraction* decision — keeping `AssimilationResult.Entities` as in-memory context for downstream agents — is conditional. We're carrying the cost of parsing and emitting entity structure on every assimilate run as an open bet that feeding structured domain data to the planner / supervisor / remediator produces better Approaches than reconstructing that structure from code in each prompt. Whether that bet pays off isn't decidable in the abstract; it will be answered by operational history once enough real adopt-and-refine cycles accumulate.

Until then, the framing holds: **usage and motivation are the valuable outputs of assimilation; structure reconstruction is a provisional helper.** When an Entity shows up in agent context, it should be in service of guiding what the agent should DO with the data (a Feature's behaviour, a Decision's rationale, a Strategy's pattern) — not re-explaining what a struct looks like when the agent can just read the file. If a later audit shows the formal extraction is burning tokens without measurably improving agent output, dropping the extraction entirely is the next step and this DJ gets superseded.
