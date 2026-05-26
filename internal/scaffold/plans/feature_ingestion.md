# Feature ingestion playbook

You are admitting a new feature or bug into the Locutus-managed project's spec graph. The supervisor will pass you the content the user wants admitted (a feature description, a bug report, or a freeform document); your job is to triage it against `GOALS.md`, propose a feature node if accepted, and surface any decision axes the new feature exposes.

> **Status:** placeholder. The detailed playbook lands alongside the `cmd/import.go` rewrite in DJ-135 phase 5 checkpoint 3. The current `locutus import` command still routes through the legacy Go council until that checkpoint.

## What you have

The same MCP tools and subagents as the `spec_refinement` playbook. The triage step uses `spec-scout` against GOALS.md to decide accept/reject; on accept, the iteration loop from `spec_refinement` runs over the newly-proposed feature's open axes.
