## DJ-042: Local-Only, No Write-Back to External Issue Trackers

**Status:** shipped

**Decision:** Locutus is local-only. `locutus import <source>` reads an external issue once and creates a local spec artifact. Locutus never writes back. Deep integration with GitHub/Jira/Linear is explicitly deferred.

**Why local-only:**
- Deep integration is a maintenance nightmare (GitHub + GitLab + Jira + Linear + Azure DevOps — each with different APIs, auth, data models)
- Write-back requires OAuth scopes, webhook handling, conflict resolution, permission management — disproportionate complexity for the value
- Local-only doesn't prevent adoption: `locutus import github#123` is a one-liner, the user already has the GitHub CLI
- The shoe project managed 26 phases of complex hardware design without issue tracker integration
- It's open source — if someone wants Jira integration, they build it

**Features are live capabilities, not tasks.** Status: `proposed`, `active`, `removed`. Never "resolved." Features represent what the product does, not work to be completed.

**Bugs tie to features and have a lifecycle:** `reported` → `triaged` → `fixing` → `fixed`. Fixed when code changes pass tests. User closes the external issue manually.
