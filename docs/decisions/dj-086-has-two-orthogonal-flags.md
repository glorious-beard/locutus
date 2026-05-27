## DJ-086: `update` Has Two Orthogonal Flags — `--reset` and `--offline`

**Status:** shipped

**Decision:** `locutus update` grows two independent flags that compose:

- `--reset` overwrites the project's scaffolded artifacts (`.borg/agents/*.md`, `.borg/workflows/*.yaml`, `.borg/models.yaml`) with the running binary's embedded versions. User content (`GOALS.md`, `.borg/spec/`, `.borg/history/`, `.borg/manifest.json`, `.locutus/`) is never modified.
- `--offline` skips the GitHub release check and download.

The four meaningful combinations:

| Command | Behavior |
| --- | --- |
| `update` | Check, download newer binary if available. Local files untouched. |
| `update --reset` | Check + download. If a download happened, refuse to reset (the running process still has old embedded artifacts) and tell the user to re-run `update --offline --reset` against the new binary. Otherwise, reset using the current binary. |
| `update --offline` | No-op with a friendly message — paired with `--reset` is the useful form. |
| `update --offline --reset` | Reset only; no network. The canonical "I just upgraded the binary, refresh my project files" command. |

**Why `--reset` is opt-in, not the default:** users edit `.borg/agents/*.md`, `.borg/workflows/*.yaml`, and `.borg/models.yaml` to tune their council and model preferences (DJ-036, DJ-080). Silently overwriting those edits on a casual binary update would surprise people. Default `update` has the narrow, predictable scope of "make the binary current"; refreshing local files requires explicit consent.

**Why two flags rather than one combined verb:** the verb-level question is "are you trying to upgrade the install?" The answer is yes either way; the flags scope what "upgrade" means in this invocation. Keeping the two operations independently togglable also covers the offline-and-reset case (which is the most common follow-up to a binary download) without inventing a third flag for it.

**Why we don't re-exec after a download to apply `--reset` immediately:** technically possible (`syscall.Exec` would replace the running process with the freshly-downloaded binary), but it's a real behavioral surprise — environment, signal handlers, and stdout/stderr buffering all behave differently across an exec. A clear "download succeeded; run `update --offline --reset` to refresh project files" message is less clever and less surprising. We can revisit if the two-step pattern becomes friction in practice.

**What `Reset` does NOT do:**

- It doesn't delete files. An agent that was in the embed in v1.0 but removed in v1.1 stays on disk after a v1.0 → v1.1 upgrade unless the user removes it manually. A future "prune" mode is its own decision.
- It doesn't touch agent files the user added that aren't in the embed (custom agents survive).
- It doesn't touch user-content directories (specs, history, manifest, runtime state, GOALS.md).

**Reversal criteria:** DJ-086 reverses only if either (a) the friction of "download finished; run --offline --reset" becomes a real complaint and re-exec ergonomics improve, or (b) we decide reset should be the default (would only happen if we had a strong story for preserving user edits across overwrite — e.g., a per-file "user-modified" flag the runtime tracks).
