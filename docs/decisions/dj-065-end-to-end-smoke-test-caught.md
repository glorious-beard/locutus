## DJ-065: End-to-End Smoke Test Caught Three Production Bugs That Mock-Only Unit Tests Had Hidden

**Status:** shipped

**Decision:** Between Parts 6 and 7 of the streaming supervision build, paused feature work and wrote a hand-rolled integration test (`internal/dispatch/live_integration_test.go`, gated behind `LOCUTUS_INTEGRATION_TEST=1`) that runs the batch dispatcher against a real Claude Code subprocess on a trivial "create hello.txt" step. The test surfaced three production bugs in the pre-existing Dispatcher path that unit tests had never run into:

1. **`ClaudeCodeDriver.BuildCommand` didn't set `--permission-mode`.** In `-p` mode Claude can't prompt, so the default `default` permission mode auto-denies any tool call that would require approval. Claude would *claim* to have created the file in its response text but never actually touch the filesystem. Fixed by adding `--permission-mode acceptEdits` (allows file edits, still gates shell/network).

2. **Worktree branch and feature branch shared the same name.** `CreateWorktree` created `locutus/<id>`; `Dispatcher.runWorkstream` later tried to merge into `locutus/<id>` — but git won't check out a branch already used by a worktree. Merge failed with `'locutus/hello' is already used by worktree at '...'`. Fixed by splitting into `locutus-wt/<id>` (scratch) and `locutus/<id>` (feature target).

3. **`WorkstreamResult.BranchName` pointed at the transient scratch branch.** That branch is deleted in `Cleanup()` after merge, so callers saw a BranchName that no longer existed. Fixed by overwriting `BranchName` with the feature branch name after successful merge.

**Why this matters as a decision:** all three bugs were catchable by a ~50-line integration test. None were catchable by the existing extensive unit-test suite because mocks substituted for the real external behavior. The lesson — "validate assumptions about external systems with a real run before stacking more layers" — is now policy: the live integration test is kept in-repo, runs via `LOCUTUS_INTEGRATION_TEST=1`, and every time the streaming path changes it's re-run against real Claude to confirm no regression.

**Alternatives considered:** continuing the per-part unit work and validating end-to-end at the end. Rejected because by the time we'd reached Part 9 with the old bugs unfixed, we'd have been debugging a four-layer interaction instead of three one-line fixes.
