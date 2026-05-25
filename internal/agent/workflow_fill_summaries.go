package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// MissingSummaryNode describes one spec node that lacks a Summary and
// needs the spec-summarizer to fill it. Populated by the prereq layer
// before the workflow runs.
type MissingSummaryNode struct {
	// Kind is the spec-node kind: "feature", "strategy", "decision",
	// "bug", "approach". Threads into the summarizer prompt and
	// selects which write-back path the merge handler takes.
	Kind string `json:"kind"`
	// ID is the spec node id (e.g. "feat-dashboard"). Used for the
	// write-back lookup and per-item identity on the result channel.
	ID string `json:"id"`
	// Path is the on-disk path the node lives at. Cached at discovery
	// time so the write-back doesn't have to recompute it.
	Path string `json:"path"`
	// Content is the JSON (feature/strategy/decision/bug) or markdown
	// body (approach) the summarizer reads. Cached at discovery time
	// so a refine racing the fill is not load-bearing — last writer
	// wins, both writes are valid Summary values.
	Content string `json:"content"`
}

// FillSummariesState is the workflow state for the fill-summaries
// pass. Populated by the SummariesPresent prereq before Run.
//
// Missing is the discovered list of nodes lacking Summary; the workflow
// fans out one slot per item. Filled and Failed track per-node
// outcomes for the aggregate-fail check at the end (populated by the
// merge handler in the orchestrator goroutine, so concurrent fanout
// slots never race on these fields).
type FillSummariesState struct {
	// Inputs (populated by prereq before Run).

	FSys          specio.FS
	SummarizerDef AgentDef
	Dispatcher    AgentDispatcher
	Missing       []MissingSummaryNode

	// Outputs (populated by mergeSummarizeResults after the fanout
	// step completes — runs in the orchestrator goroutine, single-
	// threaded with respect to RunItem invocations).

	Filled []string
	Failed map[string]error
}

// FillSummariesWorkflow runs the spec-summarizer agent against every
// node in state.Missing in parallel. Each item's RunItem closure
// invokes the summarizer, validates the output, and writes the Summary
// back to the node on disk. The Merge handler accumulates per-node
// success / failure into the state.
//
// Concurrency is bounded by the per-model `concurrent_requests` cap in
// models.yaml — Parallel:true here means "fanout in parallel up to the
// model's slot count", not "unbounded parallelism." Same envelope as
// the existing council fanouts.
//
// One workflow step. Discovery happens upstream in the prereq layer
// that walks the spec directory before invoking the workflow;
// aggregation happens downstream when the prereq checks state.Failed
// after Run returns and reports the count.
var FillSummariesWorkflow = &Workflow[FillSummariesState]{
	Snapshot: snapshotFillSummariesState,
	Rounds: []WorkflowStep[FillSummariesState]{
		{
			ID:       "summarize",
			Agents:   []string{"spec-summarizer"},
			Parallel: true,
			Fanout:   fanoutMissingSummaries,
			RunItem:  runSummarizeOne,
			Merge:    mergeSummarizeResults,
		},
	},
	MaxRounds: 1,
}

// snapshotFillSummariesState returns a shallow value copy. The slice
// fields are inputs (read-only after the prereq populates them) and
// the outputs (Filled/Failed) are written only by the merge handler in
// the orchestrator goroutine, never by parallel RunItem slots — so a
// shallow copy is correct.
func snapshotFillSummariesState(s *FillSummariesState) FillSummariesState {
	if s == nil {
		return FillSummariesState{}
	}
	return *s
}

// fanoutMissingSummaries emits one fanout item per missing-summary
// node. Each item is the JSON of MissingSummaryNode so RunItem can
// recover the per-node payload from the snapshot.
//
// Empty Missing returns (nil, nil); the executor short-circuits the
// step without dispatching. The prereq layer already short-circuits
// before invoking the workflow in this case, so this branch is
// defensive against direct callers.
func fanoutMissingSummaries(s *FillSummariesState) ([]string, error) {
	if s == nil || len(s.Missing) == 0 {
		return nil, nil
	}
	items := make([]any, 0, len(s.Missing))
	for _, n := range s.Missing {
		items = append(items, n)
	}
	return marshalFanoutItems(items)
}

// summarizeOneResult is the per-item RunItem return shape, JSON-
// serialised so the executor can carry it through RoundResult.Output
// into the merge handler. We embed the id so the merge handler can
// associate each result with its source item without depending on
// fanout-order stability.
type summarizeOneResult struct {
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
}

// runSummarizeOne is the per-item RunItem closure: invoke the
// spec-summarizer against one node's content, validate the result,
// and write the Summary back to disk. Returns the JSON of
// summarizeOneResult on success; returns a non-nil error on failure
// (which the executor surfaces as RoundResult.Err for the merge
// handler to record).
//
// Retries provider transients via the dispatcher's underlying
// RunWithRetry path (cap of 3 attempts). Bad-output errors (parse
// failures, empty summary) are surfaced as terminal failures —
// another call is unlikely to fix them within the budget; the
// operator inspects via the failed-count message and re-runs.
func runSummarizeOne(ctx context.Context, snap StateSnapshot[FillSummariesState]) (string, error) {
	st := snap.State
	var item MissingSummaryNode
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		return "", fmt.Errorf("fill-summaries: malformed fanout item: %w", err)
	}

	result, err := InvokeSpecSummarizer(ctx, st.Dispatcher, st.SummarizerDef, item.Kind, item.Content)
	if err != nil {
		return marshalSummarizeResultID(item.ID), fmt.Errorf("summarize %s: %w", item.ID, err)
	}

	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		return marshalSummarizeResultID(item.ID), fmt.Errorf("summarize %s: empty summary returned", item.ID)
	}
	if !spec.IsWellFormedSummary(summary) {
		// Soft check — log but accept. Rejecting forces another LLM
		// call to fix what is almost always a cosmetic miss (off by
		// a terminal period, slightly over length), so we take what
		// we got and let the operator notice via the warn line.
		slog.Warn("fill-summaries: summary failed soft sanity checks; accepting",
			"id", item.ID, "kind", item.Kind, "len", len(summary))
	}

	if err := writeBackSummary(st.FSys, item, summary); err != nil {
		return marshalSummarizeResultID(item.ID), fmt.Errorf("summarize %s: write-back: %w", item.ID, err)
	}

	out, mErr := json.Marshal(summarizeOneResult{ID: item.ID, Summary: summary})
	if mErr != nil {
		// Marshalling a {id, summary} pair cannot fail in practice;
		// surface defensively so the merge handler at least sees the
		// id for the failure list.
		return marshalSummarizeResultID(item.ID), fmt.Errorf("summarize %s: marshal: %w", item.ID, mErr)
	}
	return string(out), nil
}

// marshalSummarizeResultID serialises a summarizeOneResult with only
// the id populated. Used on the failure paths so the merge handler can
// still associate the round result with its source item even when the
// summary itself was never produced.
func marshalSummarizeResultID(id string) string {
	out, _ := json.Marshal(summarizeOneResult{ID: id})
	return string(out)
}

// mergeSummarizeResults runs in the orchestrator goroutine after every
// fanout slot for the step completes. Single-threaded with respect to
// state mutation, so direct field writes are safe — no mutex needed.
//
// For each RoundResult: if Err is non-nil, record the failure under
// the round's id (recovered from RoundResult.Output, which carries the
// per-item id even on failure). Otherwise record the success.
func mergeSummarizeResults(s *FillSummariesState, results []RoundResult) {
	if s == nil {
		return
	}
	if s.Failed == nil {
		s.Failed = map[string]error{}
	}
	for _, r := range results {
		var sr summarizeOneResult
		if err := json.Unmarshal([]byte(r.Output), &sr); err != nil {
			// Output is always JSON we produced (success or failure
			// path both marshal a summarizeOneResult). Unmarshal
			// failures here would indicate executor corruption.
			slog.Warn("fill-summaries: unmarshal round output", "error", err, "raw", r.Output)
			continue
		}
		if r.Err != nil {
			s.Failed[sr.ID] = r.Err
			continue
		}
		s.Filled = append(s.Filled, sr.ID)
	}
}

// writeBackSummary persists the produced Summary to the on-disk node.
// Uses specio's atomic write (tmp + rename) so a concurrent refine
// rewriting the same node lands cleanly — last writer wins, both
// payloads are valid Summary values, and the refine's full-node
// rewrite supersedes a summarizer-only write naturally because the
// refine writes the entire JSON (including its own freshly authored
// Summary).
func writeBackSummary(fsys specio.FS, item MissingSummaryNode, summary string) error {
	switch item.Kind {
	case "feature":
		return writeBackJSONSummary[spec.Feature](fsys, item.Path, summary,
			func(n *spec.Feature, s string) { n.Summary = s; n.UpdatedAt = time.Now() })
	case "strategy":
		return writeBackJSONSummary[spec.Strategy](fsys, item.Path, summary,
			func(n *spec.Strategy, s string) { n.Summary = s })
	case "decision":
		return writeBackJSONSummary[spec.Decision](fsys, item.Path, summary,
			func(n *spec.Decision, s string) { n.Summary = s; n.UpdatedAt = time.Now() })
	case "bug":
		return writeBackJSONSummary[spec.Bug](fsys, item.Path, summary,
			func(n *spec.Bug, s string) { n.Summary = s; n.UpdatedAt = time.Now() })
	case "approach":
		return writeBackApproachSummary(fsys, item.Path, summary)
	default:
		return fmt.Errorf("write-back: unknown kind %q for %q", item.Kind, item.ID)
	}
}

// writeBackJSONSummary loads a JSON-backed spec pair (basePath.json +
// basePath.md), mutates only the Summary on the typed object, and
// re-saves the pair with the body preserved verbatim. Generic over
// the node type so each kind plugs its own setter; the setter also
// bumps UpdatedAt where the type carries one.
func writeBackJSONSummary[T any](fsys specio.FS, basePath, summary string, set func(*T, string)) error {
	node, body, err := specio.LoadPair[T](fsys, basePath)
	if err != nil {
		return fmt.Errorf("load %s: %w", basePath, err)
	}
	set(&node, summary)
	if err := specio.SavePair(fsys, basePath, node, body); err != nil {
		return fmt.Errorf("save %s: %w", basePath, err)
	}
	return nil
}

// writeBackApproachSummary updates the Summary frontmatter field on an
// approach .md file, preserving the body verbatim. Approaches are
// stored as YAML frontmatter + markdown body (no JSON sidecar), so the
// round-trip goes through specio.LoadMarkdown + SaveMarkdown.
func writeBackApproachSummary(fsys specio.FS, path, summary string) error {
	node, body, err := specio.LoadMarkdown[spec.Approach](fsys, path)
	if err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	node.Summary = summary
	node.UpdatedAt = time.Now()
	if err := specio.SaveMarkdown(fsys, path, node, body); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}
