## DJ-096: State Store Lives Under `.borg/state/`, Not `.locutus/state/`

**Status:** shipped

**Decision:** The reconciliation state store relocates from `.locutus/state/` to `.borg/state/`. The DJ-068 substantive decision is unchanged — state IS committed, observed-vs-desired separation, Kubernetes-style reconciliation loop — only the on-disk path moves. A new constant `state.DefaultStateDir = ".borg/state"` is introduced; production callers reference it instead of the legacy literal.

**Why the path was wrong:** DJ-068 explicitly committed to "in-repo YAML, version-controlled, diffable, auditable via `git log`," but the implementation landed under `.locutus/state/` and `cmd/init.go` writes `.locutus/` to `.gitignore` wholesale. Net effect: state was treated as ephemeral when the design said durable. The mistake came by analogy from `.locutus/workstreams/` (DJ-073) and `.locutus/sessions/` (DJ-091), both of which genuinely are per-machine/per-run; DJ-073 even calls out the inconsistency as "a narrow departure from DJ-068's 'state is always in-repo' framing." The departure was accidentally promoted to the default.

**The clean line:**

| Directory | Lifecycle | Committed? |
| --- | --- | --- |
| `.borg/spec/` | Desired state — what should be | Yes |
| `.borg/state/` | Observed state — what currently is, hash-linked to spec | Yes (this DJ) |
| `.borg/history/` | Past-tense narrative record | Yes |
| `.borg/agents/` | Council agent prompts | Yes |
| `.borg/workflows/` | Workflow YAML | Yes |
| `.borg/models.yaml` | Model tier configuration | Yes |
| `.borg/manifest.json` | Project-root marker | Yes |
| `.locutus/sessions/` | LLM call traces (per-run debug) | No |
| `.locutus/workstreams/` | In-flight execution coordination (deleted on terminal) | No |

`.borg/` = "what the project knows about itself." `.locutus/` = "what this run / this machine is doing right now." Naming actually fits the Star Trek allusion — `.borg/` is the Collective's accumulated knowledge; `.locutus/` is the assimilated speaker's working memory.

**Why state belongs alongside spec:** state is the observed counterpart to `.borg/spec/`'s desired state — they're sibling project-truths. A teammate cloning the repo immediately sees what's `live`, what's `drifted`, what's `unplanned`. `git blame .borg/state/app-oauth-login.yaml` gives the audit trail of when reconciliation last passed and what spec_hash it asserted against. CI can compare committed claims against a fresh reconcile and flag stale state as a build failure.

**Why per-machine concerns don't change the answer:** artifact hashes target source files (`src/auth/oauth.go`), which are deterministic across environments. Built binaries are NEVER part of artifact hashes — only the source paths the spec asserts.

**Migration:** none. The legacy `.locutus/state/` path was gitignored, so existing projects (winplan being the only one) have nothing committed to preserve. The next adopt run regenerates entries at the new path. Pre-alpha; no consumer relies on the old layout.

**What's new:**

- `state.DefaultStateDir = ".borg/state"` constant in [internal/state/store.go](../internal/state/store.go).
- Production callers ([cmd/refine.go](../cmd/refine.go), [cmd/adopt.go](../cmd/adopt.go)) reference `state.DefaultStateDir` instead of the literal.
- Test fixtures use `.borg/state` literals (test-isolated, no need to touch the production constant).
- Scaffolder ([internal/scaffold/scaffold.go](../internal/scaffold/scaffold.go)) creates `.borg/state` instead of `.locutus/state`.
- Comment in [internal/state/state.go](../internal/state/state.go) corrected from `.locutus/state` to `.borg/state`.

**What stays the same:**

- DJ-068's substantive decisions (state is committed; observed-vs-desired separation; Kubernetes-style loop; per-Approach state entries; per-file artifact hashes; lifecycle states).
- DJ-073's gitignored treatment of `.locutus/workstreams/` (genuinely transient execution coordination).
- DJ-091's gitignored treatment of `.locutus/sessions/` (per-run debug traces).
- DJ-081's `.borg/manifest.json` as the project-root marker (unchanged content; just a marker).
- The `cmd/init.go` `.gitignore` writer continues to add `.locutus/` whole — now correct without exceptions, since nothing committed lives under that path.

**Reversal criteria:** revert if the spec/state co-location creates a category of merge conflict on a real team workflow we haven't anticipated. Pre-alpha; no measurement yet, but the failure mode is bounded — state files are small, deterministic on source artifacts, and `git merge` handles them as ordinary YAML.

**Reference:** supersedes the path detail in DJ-068. Substantive decisions in DJ-068 stand unchanged. Followup to DJ-095's general Phase-4 work, but logically independent.
