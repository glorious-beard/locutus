package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// ToolRegistry is the Locutus-owned tool surface adapters dispatch
// against. Adapters drive the per-provider tool-use loop; the
// registry's only job is to map a tool name to its description,
// input schema, and handler. Callers (cmd/llm.go) register tools
// once at startup and pass the registry to NewExecutor.
//
// Tool names appear verbatim in agent .md frontmatter
// (`tools: [spec_list_manifest, spec_get]`). The executor looks
// each up here and threads the resolved adapters.ToolDef into the
// adapters.Request — adapters never see the registry directly.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]adapters.ToolDef
}

// NewToolRegistry returns an empty registry. Production wiring
// constructs one per Executor, so test harnesses get an isolated
// registry and don't share global state.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]adapters.ToolDef{}}
}

// Register adds (or replaces) a tool entry. Re-registering the
// same name overrides the prior entry — useful for tests; in
// production each tool should be registered exactly once at
// startup.
func (r *ToolRegistry) Register(def adapters.ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = def
}

// Resolve returns the registered tool for name, or ok=false if
// none is registered. The executor surfaces missing tools as a
// build-time error before dispatch.
func (r *ToolRegistry) Resolve(name string) (adapters.ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.tools[name]
	return def, ok
}

// Names returns the set of registered tool names in arbitrary
// order. Useful for diagnostics ("tools available: …").
func (r *ToolRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

// TypedHandler wraps a strongly-typed Go handler so callers can
// register tools without hand-rolling JSON marshalling on both
// ends. In is the input shape the model emits (the JSON arguments
// of the tool call); Out is what the handler returns to feed back
// as the tool result on the next turn.
//
// Errors from Unmarshal / Marshal are surfaced as tool errors so
// the model can see "your input didn't match the schema" rather
// than the call failing opaquely.
func TypedHandler[In any, Out any](fn func(ctx context.Context, in In) (Out, error)) func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var in In
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("tool input parse: %w", err)
			}
		}
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
}
