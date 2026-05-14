package acp

// AgentSpawns is the canonical registry of agent-id → ACP-server-subprocess
// spawn descriptor. The supervisor's OpenConn function in cmd/adopt.go uses
// this to dispatch coding work; `locutus init`'s preflight check uses it to
// verify the required binaries are reachable on $PATH.
//
// Install incantations per agent (kept in sync with preflightHints below):
//
//   - claude-code: `npm i -g @agentclientprotocol/claude-agent-acp` installs a
//     `claude-agent-acp` shim that speaks ACP over stdio.
//   - codex: GitHub-release tarball drops a `codex-acp` binary. NOT on
//     crates.io (Phase 0 verification of DJ-119 confirmed `cargo install
//     codex-acp` fails).
//   - gemini: the official Google Gemini CLI gains ACP via the `--acp` flag.
//
// Mutating this map is a public-surface change — every consumer of
// `locutus adopt` is affected. Adding a new agent here without a matching
// entry in the preflight hint table will produce a "binary missing, no
// install hint available" report; keep the two in lockstep.
var AgentSpawns = map[string]Spawn{
	"claude-code": {Cmd: "claude-agent-acp"},
	"codex":       {Cmd: "codex-acp"},
	"gemini":      {Cmd: "gemini", Args: []string{"--acp"}},
}
