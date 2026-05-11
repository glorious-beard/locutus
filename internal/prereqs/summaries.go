package prereqs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SummariesContext threads the dependencies the prereq needs into a
// single call site. Callers populate it from the command layer.
//
// Dispatcher is required when regen=true (the resolution path runs an
// LLM workflow). When regen=false the prereq does pure I/O and the
// dispatcher may be nil.
type SummariesContext struct {
	// FSys is the project filesystem; must be rooted at the project's
	// `.borg/` parent.
	FSys specio.FS
	// Executor is the workflow executor used to dispatch
	// spec_summarizer fanout. Required when regen=true.
	Executor agent.AgentExecutor
	// Dispatcher is the per-call dispatcher used by the workflow's
	// RunItem closures. Required when regen=true. Production callers
	// pass agent.NewDispatcher(executor); tests can swap in mocks.
	Dispatcher agent.AgentDispatcher
}

// SummariesError is returned by EnsureSpecsContainSummaries when the
// assertion fails (regen=false and nodes are missing summaries, OR
// regen=true and the resolution workflow couldn't fill every node).
//
// The error carries the count + per-id details so the calling verb can
// surface an actionable message ("rerun `locutus update --check-pre-reqs`").
type SummariesError struct {
	Missing []string         // node IDs lacking Summary (regen=false case)
	Failed  map[string]error // node IDs the workflow couldn't fill (regen=true case)
}

func (e *SummariesError) Error() string {
	if len(e.Missing) > 0 {
		return fmt.Sprintf("%d spec nodes missing Summary; rerun `locutus update --check-pre-reqs` to fill", len(e.Missing))
	}
	if len(e.Failed) > 0 {
		return fmt.Sprintf("%d spec nodes could not be summarized; rerun `locutus update --check-pre-reqs` to retry", len(e.Failed))
	}
	return "spec summaries prereq failed"
}

// EnsureSpecsContainSummaries is the prereq for "every persisted spec
// node has a non-empty Summary." When regen=false it walks the spec
// directory and returns a SummariesError listing the ids of any nodes
// without Summary. When regen=true it additionally dispatches the
// FillSummariesWorkflow to fill the missing summaries via the
// spec_summarizer fast-tier agent, returning a SummariesError only if
// the workflow leaves any nodes unfilled.
//
// Greenfield projects (no spec dirs, zero nodes) pass trivially.
//
// Concurrency: safe to call concurrently against different projects.
// Within a single project, concurrent invocations may both attempt to
// fill the same node; the file-level atomic write means the later
// finisher wins, both Summary values are valid, and downstream
// operations see a populated field either way.
func EnsureSpecsContainSummaries(ctx context.Context, sctx SummariesContext, regen bool) error {
	if sctx.FSys == nil {
		return fmt.Errorf("EnsureSpecsContainSummaries: nil FSys")
	}

	missing, err := discoverMissingSummaries(sctx.FSys)
	if err != nil {
		return fmt.Errorf("discover missing summaries: %w", err)
	}
	if len(missing) == 0 {
		return nil
	}

	if !regen {
		ids := make([]string, 0, len(missing))
		for _, n := range missing {
			ids = append(ids, n.ID)
		}
		return &SummariesError{Missing: ids}
	}

	if sctx.Executor == nil || sctx.Dispatcher == nil {
		return fmt.Errorf("EnsureSpecsContainSummaries: regen=true requires Executor and Dispatcher")
	}

	slog.Info("prereq: filling missing spec summaries",
		"count", len(missing),
		"agent", "spec_summarizer")

	def, err := scaffold.LoadAgent(sctx.FSys, "spec_summarizer")
	if err != nil {
		return fmt.Errorf("load spec_summarizer agent: %w", err)
	}

	state := agent.FillSummariesState{
		FSys:          sctx.FSys,
		SummarizerDef: def,
		Dispatcher:    sctx.Dispatcher,
		Missing:       missing,
	}

	exec := &agent.WorkflowExecutor[agent.FillSummariesState]{
		Executor:  sctx.Executor,
		AgentDefs: map[string]agent.AgentDef{"spec_summarizer": def},
		Workflow:  agent.FillSummariesWorkflow,
	}

	if _, err := exec.Run(ctx, &state); err != nil {
		// The workflow itself returns nil even when individual items
		// fail (the failures land on state.Failed). A non-nil err here
		// is something more catastrophic — surface it directly rather
		// than wrapping in SummariesError.
		return fmt.Errorf("fill-summaries workflow: %w", err)
	}

	if len(state.Failed) > 0 {
		slog.Warn("prereq: some spec summaries could not be filled",
			"failed", len(state.Failed),
			"filled", len(state.Filled))
		return &SummariesError{Failed: state.Failed}
	}

	slog.Info("prereq: spec summaries filled",
		"count", len(state.Filled))
	return nil
}

// discoverMissingSummaries walks the spec directories and returns one
// MissingSummaryNode per node whose Summary field is empty after
// whitespace trim. Order is stable (sorted by directory walk) so the
// downstream fanout produces deterministic per-id results for the
// failure-list aggregate check.
//
// Missing kind directories (greenfield) yield empty results, not
// errors. Malformed individual files are skipped with a slog.Warn —
// they're a different surface than missing summaries and the user's
// verb will hit them too if it loads the spec graph.
func discoverMissingSummaries(fsys specio.FS) ([]agent.MissingSummaryNode, error) {
	var out []agent.MissingSummaryNode

	if pairs, err := specio.WalkPairs[spec.Feature](fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("discover missing summaries: malformed feature", "path", p.Path, "error", p.Err)
				continue
			}
			if spec.HasSummary(p.Object.Summary) {
				continue
			}
			content, cErr := loadJSONContent(fsys, p.Path)
			if cErr != nil {
				slog.Warn("discover missing summaries: feature content read failed", "id", p.Object.ID, "error", cErr)
				continue
			}
			out = append(out, agent.MissingSummaryNode{
				Kind:    "feature",
				ID:      p.Object.ID,
				Path:    p.Path,
				Content: content,
			})
		}
	}

	if pairs, err := specio.WalkPairs[spec.Strategy](fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("discover missing summaries: malformed strategy", "path", p.Path, "error", p.Err)
				continue
			}
			if spec.HasSummary(p.Object.Summary) {
				continue
			}
			content, cErr := loadJSONContent(fsys, p.Path)
			if cErr != nil {
				slog.Warn("discover missing summaries: strategy content read failed", "id", p.Object.ID, "error", cErr)
				continue
			}
			out = append(out, agent.MissingSummaryNode{
				Kind:    "strategy",
				ID:      p.Object.ID,
				Path:    p.Path,
				Content: content,
			})
		}
	}

	if pairs, err := specio.WalkPairs[spec.Decision](fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("discover missing summaries: malformed decision", "path", p.Path, "error", p.Err)
				continue
			}
			if spec.HasSummary(p.Object.Summary) {
				continue
			}
			content, cErr := loadJSONContent(fsys, p.Path)
			if cErr != nil {
				slog.Warn("discover missing summaries: decision content read failed", "id", p.Object.ID, "error", cErr)
				continue
			}
			out = append(out, agent.MissingSummaryNode{
				Kind:    "decision",
				ID:      p.Object.ID,
				Path:    p.Path,
				Content: content,
			})
		}
	}

	if pairs, err := specio.WalkPairs[spec.Bug](fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err != nil {
				slog.Warn("discover missing summaries: malformed bug", "path", p.Path, "error", p.Err)
				continue
			}
			if spec.HasSummary(p.Object.Summary) {
				continue
			}
			content, cErr := loadJSONContent(fsys, p.Path)
			if cErr != nil {
				slog.Warn("discover missing summaries: bug content read failed", "id", p.Object.ID, "error", cErr)
				continue
			}
			out = append(out, agent.MissingSummaryNode{
				Kind:    "bug",
				ID:      p.Object.ID,
				Path:    p.Path,
				Content: content,
			})
		}
	}

	if files, err := fsys.ListDir(".borg/spec/approaches"); err == nil {
		for _, f := range files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			obj, body, err := specio.LoadMarkdown[spec.Approach](fsys, f)
			if err != nil {
				slog.Warn("discover missing summaries: malformed approach", "path", f, "error", err)
				continue
			}
			if spec.HasSummary(obj.Summary) {
				continue
			}
			out = append(out, agent.MissingSummaryNode{
				Kind:    "approach",
				ID:      obj.ID,
				Path:    f,
				Content: body,
			})
		}
	}

	return out, nil
}

// loadJSONContent reads basePath.json and returns the canonical JSON
// content the summarizer agent sees. Read directly (not via LoadPair)
// because the agent reads raw JSON, not a pre-deserialised struct,
// and the file is the source of truth for the node's content.
func loadJSONContent(fsys specio.FS, basePath string) (string, error) {
	data, err := fsys.ReadFile(basePath + ".json")
	if err != nil {
		return "", err
	}
	// Re-marshal with indent to keep prompt formatting stable across
	// nodes that may have been hand-edited with different indentation.
	var anyVal any
	if err := json.Unmarshal(data, &anyVal); err != nil {
		return string(data), nil //nolint:nilerr // pass raw on parse failure
	}
	pretty, err := json.MarshalIndent(anyVal, "", "  ")
	if err != nil {
		return string(data), nil //nolint:nilerr
	}
	return string(pretty), nil
}
