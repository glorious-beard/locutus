## DJ-032: Commit-Per-Workstream on a Local Feature Branch (Reframed 2026-04-25)

**Status:** shipped (2026-04-25 reframe)

**Decision:** Each workstream produces commits on a scratch branch (`locutus-wt/<workstream-id>`), then merges into a local feature branch (`locutus/<workstream-id>`) — no PR objects, no automated review hop, no remote push. The human reviews the accumulated local state when work is done (git log, diff, run the app) and pushes when satisfied.

**Why no PR objects:** Locutus is a local, opinionated tool today. PRs are a team-facing artifact — they exist for review by other humans on a remote-hosted forge (GitHub, GitLab, Gitea). Locutus has only one operator. Creating PRs that no one will look at is waste, and an automated "Locutus reviews its own PR before merging" hop is just code obscuring what's already a pure local merge. The cleanest abstraction is what the dispatcher already does: commit, merge to feature branch, move on.

**Why not auto-push:** Pushing to remote is irreversible. The user needs a review point before changes leave the machine. Local auto-merge gives Locutus full autonomy during execution while keeping the user in control of what becomes visible elsewhere.

**The model (as implemented):**

- `dispatch.runWorkstream` creates a worktree on `locutus-wt/<ws-id>`, runs the agent, commits the result, then `MergeToFeatureBranch("locutus/<ws-id>")`.
- Multiple workstreams in a plan each produce their own feature branch.
- Work flows continuously — no per-workstream human halts.
- User reviews `git log`, `git diff`, runs the app, decides what to push.
- User pushes when satisfied, or resets / cherry-picks / amends when not.

**Reframe note (2026-04-25):** the original DJ called this "PR-Per-Workstream" with an implied auto-review hop. That language was aspirational and never reached implementation — the dispatcher always did local commit-and-merge. The new framing aligns vocabulary with reality. The team-facing pivot (real PR creation, automated reviewer agent against the PR diff, structural test-first enforcement at plan time) is **deferred** — see DJ-038, DJ-040, and the gap-closeout plan's Round 8 deferral.

**Scope of "shipped":** the local commit-and-merge mechanism. Quality gates that should fire on the merged feature branch (spec alignment vs. `traces.json`, no-stubs check, interface-contract satisfaction) are tracked separately and run in the verify phase of `adopt`, not as a PR review. If/when Locutus pivots team-facing, those checks become inputs to the PR-level reviewer.
