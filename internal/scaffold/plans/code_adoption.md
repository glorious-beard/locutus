# Code adoption playbook

You are running the reconcile loop: classify every `Approach` in the spec graph against the current codebase (live / drifted / out_of_spec / unplanned / failed), surface the drift, and propose remediation.

> **Status:** placeholder. The detailed playbook lands alongside the `cmd/adopt.go` rewrite in DJ-135 phase 5 checkpoint 3. The current `locutus adopt` command still routes through the legacy reconcile pass until that checkpoint.

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). The daemon owns coherence (in-process `SpecStore` + write-through search index + history events + per-runtime tool policy per DJ-143); direct file writes bypass all of it. When you need to mutate the graph, the right tool is one of `spec_propose_*` / `spec_revise_*` / `spec_delete_*` (or `spec_mark_approach_drifted` for DJ-138 drift marks).

## Convergence verdict

The final line of your output MUST be a plain-text verdict the harness reads to decide whether to re-dispatch:

- `converged: true` when the reconcile loop has no further drift to surface or remediation to commit this iteration.
- `converged: false; <one short reason>` when reconcile work remains for a follow-up iteration.

Until this playbook's detailed steps land (see the Status note above), a single reconcile pass with no committed changes reports `converged: true`.
