# Code adoption playbook

You are running the reconcile loop: classify every `Approach` in the spec graph against the current codebase (live / drifted / out_of_spec / unplanned / failed), surface the drift, and propose remediation.

> **Status:** placeholder. The detailed playbook lands alongside the `cmd/adopt.go` rewrite in DJ-135 phase 5 checkpoint 3. The current `locutus adopt` command still routes through the legacy reconcile pass until that checkpoint.

## Convergence verdict

The final line of your output MUST be a plain-text verdict the harness reads to decide whether to re-dispatch:

- `converged: true` when the reconcile loop has no further drift to surface or remediation to commit this iteration.
- `converged: false; <one short reason>` when reconcile work remains for a follow-up iteration.

Until this playbook's detailed steps land (see the Status note above), a single reconcile pass with no committed changes reports `converged: true`.
