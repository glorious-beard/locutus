package cmd

import (
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// getLLM returns a Genkit-backed LLM for commands that need it. Returns a
// typed error when no provider is configured so callers can render a friendly
// message; production commands should guard against this case and point the
// user at the env vars Genkit reads.
//
// Most subcommands should use recordingLLM instead — getLLM is the raw
// inner LLM with no session transcript. Tests and dispatch (which has its
// own logging) are the legitimate raw consumers.
func getLLM() (agent.LLM, error) {
	if !agent.LLMAvailable() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s, %s, or %s",
			agent.EnvKeyAnthropicAPI, agent.EnvKeyGeminiAPI, agent.EnvKeyGoogleAPI,
		)
	}
	return agent.NewGenKitLLM()
}

// recordingLLM wraps getLLM with a SessionRecorder so every council /
// spec-generation LLM call is captured to .locutus/sessions/<sid>.yaml
// for after-the-fact inspection. command is recorded in the file
// metadata for human reference (e.g. "refine goals", "import docs/x.md").
//
// Returns the wrapped LLM, the recorder (so callers can log the session
// path to stdout), and any error from constructing either.
func recordingLLM(fsys specio.FS, root, command string) (agent.LLM, *agent.SessionRecorder, error) {
	inner, err := getLLM()
	if err != nil {
		return nil, nil, err
	}
	rec, err := agent.NewSessionRecorder(fsys, command, root)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewLoggingLLM(inner, rec), rec, nil
}
