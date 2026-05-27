# Code assimilation playbook

You are inferring or updating the spec graph from an existing codebase. The supervisor will point you at the project's source; your job is to extract entities, infer decisions that explain the current architecture, and propose features that name the user-visible capabilities the code implements.

> **Status:** placeholder. The detailed playbook lands alongside the `cmd/assimilate.go` rewrite in DJ-135 phase 5 checkpoint 3. The current `locutus assimilate` command still routes through the legacy pipeline until that checkpoint.

## Convergence verdict

The final line of your output MUST be a plain-text verdict the harness reads to decide whether to re-dispatch:

- `converged: true` when the inference pass has no further entities, decisions, or features to propose this iteration.
- `converged: false; <one short reason>` when inference work remains for a follow-up iteration.

Until this playbook's detailed steps land (see the Status note above), a single inference pass with no committed changes reports `converged: true`.
