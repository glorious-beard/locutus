package mcp

import (
	"context"
	"fmt"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool descriptions for the read surface. Mirrors the equivalents
// registered against the council's in-process ToolRegistry in
// internal/agent/spec_tools.go (RegisterSpecTools) — same SpecStore
// underneath, same external contract, different registration call
// site. Per DJ-134 the tool-behavior text lives here (registration),
// not in agent prompts.
const (
	descSpecListManifest = "Returns a compact index of every spec node grouped by kind (goals, antigoals, features, strategies, decisions, bugs, approaches). Each entry carries id, title, optional kind (for strategies), a one-line summary truncated to ~200 chars, an `origin` field (`settled` for nodes loaded from .borg/spec/ on session start, `proposed` for nodes added or revised in flight), and a `working` flag (true when an active write is rewriting the node — its body may change before the next read). Use this to navigate the spec graph without dumping every node's full content; pair it with spec_get when you need bodies. The response also carries two manifest-level metadata fields (DJ-139): `goals_md_hash` is the sha256:<hex> digest of GOALS.md as of the most recent successful sync, and `goals_md_synced_at` is the RFC3339 timestamp of that sync. The Phase 6 `refine goals` orchestrator hashes GOALS.md at run start and compares against `goals_md_hash` to decide whether to short-circuit the diff-and-apply matcher when GOALS.md hasn't changed since the last sync. Empty hash + zero timestamp signal `no previous sync — do the full bootstrap pass` (legacy or greenfield project)."

	descSpecGet = "Returns the full bodies of N spec nodes by id in one call. Input is `{ids: [string]}` — an array of ids; passing a single id is valid as a one-element array. Output is `{results, available_ids?, working?}`: `results` is `{id: {status, body?, working?, reason?}}` with every requested id present exactly once. status is `settled` (id resolved on-disk-loaded body), `in_flight` (id resolved in-memory proposed body), or `missing` (id couldn't be resolved — reason names the failure). For misses, `available_ids` carries the per-kind id catalogue (e.g. `{decision: [\"dec-a\",\"dec-b\"]}`) surfaced once per kind regardless of how many ids of that kind missed. PREFER one batched call over N sequential single-id calls."

	descSpecSearch = "Returns the ranked top-N spec nodes matching a free-text query (BM25 over title/summary/body, with optional kind filter). Use for topic-scoped lookups (e.g. \"what do we have on authentication?\"); prefer spec_list_manifest when you need to enumerate the full graph structure. Phrase queries via double-quotes (e.g. \"row level security\") and trailing-* prefix queries (e.g. auth*) are supported. Each hit carries id, title, kind, optional summary, and BM25 score."
)

// specGetInput mirrors agent.SpecGetInput. The MCP SDK's
// jsonschema-go tag syntax is bare-string (used verbatim as the
// field description), so tags here carry prose only — enum and
// minItems constraints would need an explicit InputSchema map. We
// rely on SpecStore.Put's id-prefix validation and the handler's
// explicit empty-ids check as the structural backstop.
type specGetInput struct {
	IDs []string `json:"ids" jsonschema:"Array of spec node ids with known prefixes (feat-, strat-, dec-, bug-, app-). Pass every id you need in one call; empty arrays are rejected."`
}

// specSearchInput is the MCP-side input for spec_search.
type specSearchInput struct {
	Query string `json:"query" jsonschema:"Free-text query. Phrase queries via double-quotes (e.g. \"row level security\") and trailing-* prefix queries (e.g. auth*) are supported."`
	Kind  string `json:"kind,omitempty" jsonschema:"Optional kind filter. One of: feature, strategy, decision, bug, approach. Omitted means all kinds."`
	Limit int    `json:"limit,omitempty" jsonschema:"Optional cap on returned hits. Default 20, max 100 (values outside this range are clamped). The total_matches field signals when the slice is truncated."`
}

// specSearchHit is the MCP-side per-hit shape. Smaller than
// agent.SpecSearchHit — no per-field BM25 diagnostics for the MCP
// surface. Coding agents reasoning over the manifest don't need the
// fine-grained match attribution that the council's elaborator relied
// on.
type specSearchHit struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Kind    string  `json:"kind,omitempty"`
	Summary string  `json:"summary,omitempty"`
	Score   float64 `json:"score"`
}

type specSearchOutput struct {
	Hits         []specSearchHit `json:"hits"`
	TotalMatches int             `json:"total_matches"`
}

const (
	specSearchDefaultLimit = 20
	specSearchMaxLimit     = 100
)

// registerReadTools wires the three read tools onto the server.
// Handler bodies are thin — the SpecStore methods do the work.
//
// Per DJ-147 Task 6, each handler consults the calling session's
// OverlayView so dry-run sessions surface their own would-be writes
// on the read path: overlay-only entries appear, overlay-deleted
// entries report missing, overlay-revised entries return the new
// body. Non-dry-run sessions get an overlay-nil view whose calls pass
// straight through to the base store (byte-compatible with the
// pre-task behavior).
func registerReadTools(server *mcp.Server, store *agent.SpecStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_list_manifest",
		Description: descSpecListManifest,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, agent.SpecManifest, error) {
		return nil, store.OverlayView(req.Session).Manifest(), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_get",
		Description: descSpecGet,
	}, func(_ context.Context, req *mcp.CallToolRequest, in specGetInput) (*mcp.CallToolResult, agent.SpecGetResult, error) {
		if len(in.IDs) == 0 {
			return nil, agent.SpecGetResult{}, fmt.Errorf("spec_get: ids must be a non-empty array")
		}
		return nil, store.OverlayView(req.Session).GetSpec(in.IDs), nil
	})

	// spec_search currently runs query base-only. Per DJ-147 §3 +
	// Resolved Q5 the documented fidelity gap is acceptable: full-text
	// matches against captured-but-not-indexed bodies are best-effort.
	// A future refinement could post-filter view.Captured() to inject
	// matching captured bodies into the result; for now we accept that
	// search rankings on a dry-run session reflect the base store only.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_search",
		Description: descSpecSearch,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in specSearchInput) (*mcp.CallToolResult, specSearchOutput, error) {
		if in.Query == "" {
			return nil, specSearchOutput{}, fmt.Errorf("spec_search: query is required")
		}
		limit := in.Limit
		if limit <= 0 || limit > specSearchMaxLimit {
			limit = specSearchDefaultLimit
		}
		hits, total, err := store.Search(in.Query, search.Options{Kind: in.Kind, Limit: limit})
		if err != nil {
			return nil, specSearchOutput{}, fmt.Errorf("spec_search: %w", err)
		}
		out := specSearchOutput{TotalMatches: total}
		for _, h := range hits {
			out.Hits = append(out.Hits, specSearchHit{
				ID:    h.ID,
				Title: h.Title,
				Kind:  h.Kind,
				Score: h.Score,
			})
		}
		return nil, out, nil
	})
}
