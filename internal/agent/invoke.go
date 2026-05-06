package agent

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/specio"
)

// NamedAgentFn loads the agent definition at `.borg/agents/<agentID>.md`
// and returns a closure that invokes it with a user prompt, using the
// loaded system prompt as the agent's persona. Per DJ-036 (shipped)
// agent identities live in prompt files, not hard-coded strings;
// NamedAgentFn is the shared plumbing for callers that need to invoke a
// single named agent with a prompt payload.
//
// Downstream packages that want a richer type (e.g.
// history.GenerateFn) convert the returned function via Go's named-type
// conversion — the underlying signature is the same.
//
// Returns an error if the agent def cannot be loaded or is not found.
func NamedAgentFn(fsys specio.FS, exec AgentExecutor, agentID string) (func(ctx context.Context, userPrompt string) (string, error), error) {
	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	if err != nil {
		return nil, fmt.Errorf("load agent defs: %w", err)
	}
	var def *AgentDef
	for i := range defs {
		if defs[i].ID == agentID {
			def = &defs[i]
			break
		}
	}
	if def == nil {
		return nil, fmt.Errorf("agent %q not found under .borg/agents", agentID)
	}
	bound := *def
	return func(ctx context.Context, user string) (string, error) {
		input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}
		resp, err := exec.Run(ctx, bound, input)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}, nil
}
