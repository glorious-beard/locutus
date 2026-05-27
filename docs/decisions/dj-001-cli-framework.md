## DJ-001: CLI Framework

**Status:** shipped

**Decision:** Use `alecthomas/kong` instead of `spf13/cobra`.

**Alternatives considered:**
- Cobra — industry standard, but more boilerplate
- urfave/cli — simpler API
- No framework (stdlib `flag`)

**Why Kong:** User preference. Kong's struct-based command definitions are a cleaner fit for the `--json` and `--verbose` flag pattern, where global flags live on a parent struct and are naturally inherited by subcommands.
