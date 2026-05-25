package mcp

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/activity"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// promptPersona is the system-level framing every Locutus activity
// prompt carries on its Description field. Per DJ-135 phase 5 Q1
// answer (b), the prompt surface splits persona (here) from the
// per-activity playbook body (Messages). MCP itself doesn't have a
// system role; description fills that conceptual gap and is rendered
// in client prompt-picker UIs.
const promptPersona = "You are executing a Locutus activity playbook. The Locutus MCP server (this connection) exposes the spec graph via spec_* tools and the spec://manifest resource; published subagents under the locutus/ namespace are your council. Follow the playbook below and call back into the spec_* tools to mutate the graph. Read the playbook end-to-end before starting work."

// registerActivityPrompts wires one MCP prompt per registered
// activity whose playbook exists in .borg/plans/. Activities
// without a plan file are skipped — the prompt would be empty and
// the client would have nothing to render.
//
// Each prompt's name equals the activity name (snake_case, matches
// MCP-prompt convention). Clients group prompts by their server's
// implementation name ("locutus"), so the "locutus." namespace
// prefix the plan originally specified is implicit and not added
// here.
func registerActivityPrompts(server *mcp.Server, fsys specio.FS, reg *activity.Registry) error {
	if fsys == nil || reg == nil {
		return nil
	}
	for _, name := range reg.Names() {
		body, ok, err := loadPlanBody(fsys, name)
		if err != nil {
			return fmt.Errorf("registerActivityPrompts: %s: %w", name, err)
		}
		if !ok {
			continue
		}
		act, _ := reg.Lookup(name)
		server.AddPrompt(&mcp.Prompt{
			Name:        act.Name,
			Description: promptPersona,
		}, makePromptHandler(act.Name, body))
	}
	return nil
}

// makePromptHandler binds one prompt name and body to a
// PromptHandler that returns the body as a single user-role
// message. Per Q1 (b), the system persona lives on Prompt.Description;
// the playbook body lives on the user message.
//
// Closure capture: name + body are captured at registration time,
// so plan-file edits don't reflect until the daemon restarts. This
// matches the Phase 4 publisher model (re-emission on
// `locutus update --reset`) — playbook revisions are a deliberate
// operation, not a hot-reload.
func makePromptHandler(name, body string) mcp.PromptHandler {
	return func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: fmt.Sprintf("Locutus %s activity playbook", name),
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: body},
				},
			},
		}, nil
	}
}

// loadPlanBody is a small wrapper around .borg/plans/<name>.md reads
// that returns (body, false, nil) on missing file rather than an
// error. Lifted out of the canonical loader's loadPlan to keep the
// MCP-side surface free of a publisher-package dependency.
func loadPlanBody(fsys specio.FS, activityName string) (string, bool, error) {
	path := ".borg/plans/" + activityName + ".md"
	data, err := fsys.ReadFile(path)
	if err != nil {
		// Match the publisher's isNotExist semantics — both OSFS
		// (*PathError → fs.ErrNotExist) and MemFS ("file does not
		// exist" string sentinel) need to count.
		if isPlanFileNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), true, nil
}
