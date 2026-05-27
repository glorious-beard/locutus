## DJ-081: Project-Root Walk-Up for All Subcommands Except `init`

**Status:** shipped

**Decision:** Every subcommand except `init` resolves its filesystem root by walking up from the current working directory until it finds `.borg/manifest.json` (the marker scaffolded by `init`). Reaching the filesystem root without finding it returns `ErrNotInProject` with a friendly "run `locutus init` here, or cd into an existing project" message. `init` deliberately stays cwd-rooted because that's the bootstrap step.

**Why walk-up, not cwd-only:** before this, running any subcommand from a subdirectory would either error (no `.borg/`) or write a fresh `.borg/` in the wrong place. Standard tools (git, cargo, npm) walk up; users expect Locutus to do the same.

**Why `.borg/manifest.json` as the marker:** it's persistent (lifetime of the project), already written by `init`, and the JSON content carries authoritative metadata so a misplaced empty `.borg/` directory doesn't masquerade as a project root.
