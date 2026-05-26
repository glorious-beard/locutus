# Code adoption playbook

You are running the reconcile loop: classify every `Approach` in the spec graph against the current codebase (live / drifted / out_of_spec / unplanned / failed), surface the drift, and propose remediation.

> **Status:** placeholder. The detailed playbook lands alongside the `cmd/adopt.go` rewrite in DJ-135 phase 5 checkpoint 3. The current `locutus adopt` command still routes through the legacy reconcile pass until that checkpoint.
