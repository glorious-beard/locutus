package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool descriptions for the write surface. Phase 1 ships four write
// tools: propose_{decision,feature,strategy} and revise_decision.
// Per DJ-134 the registered description is the agent-facing reference
// for what each tool does and what input shape it expects.
//
// Transaction model is implicit auto-commit per tool call: each
// handler opens a SpecStore transaction, applies the Put, commits,
// and notifies subscribers. The MCP client never sees transaction
// state.
const (
	descSpecProposeDecision = "Propose a new architectural decision OR revise an existing one (upsert semantics on id). Input is the full decision body; the server fills created_at + updated_at = now (use spec_revise_decision instead when you need to preserve the original created_at). The id must use the dec- prefix; per DJ-133 the id equals dec-<axis-id> where <axis-id> names the question being answered (e.g. dec-oltp-store, dec-auth-provider). On success the manifest is persisted to .borg/spec/decisions/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecProposeFeature = "Propose a new product feature (upsert semantics on id). Input is the full feature body — the server fills created_at + updated_at = now. The id must use the feat- prefix. decisions is required and lists the decision ids this feature depends on; every entry must reference a decision present in the graph. On success the manifest is persisted to .borg/spec/features/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecProposeStrategy = "Propose a new engineering strategy (upsert semantics on id). Input is the full strategy body. The id must use the strat- prefix. decisions is required and lists the decision ids this strategy depends on; every entry must reference a decision present in the graph. On success the manifest is persisted to .borg/spec/strategies/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecReviseDecision = "Revise an existing architectural decision. Same input shape as spec_propose_decision, but the id MUST already exist — the server preserves the original created_at and bumps updated_at to now. Use this when modifying a decision that has been published (versus spec_propose_decision which resets the timestamps). On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."
)

// Input schemas mirror spec.Decision / spec.Feature / spec.Strategy
// at the field level — agent-authored full bodies. Server-managed
// fields (created_at, updated_at, locked) are omitted from the input
// schema and filled in handler code so the agent's surface stays
// focused on the semantic content.
//
// The MCP SDK's jsonschema-go tag is bare-string (description only).
// Constraints that the invopop syntax would express (enum, minItems)
// are documented inline in the description so the model still sees
// them; structural enforcement happens at the handler layer (where
// SpecStore.Put's type-assert + id-prefix check catches violations).
type proposeDecisionInput struct {
	ID           string            `json:"id" jsonschema:"Decision id with dec- prefix; per DJ-133 equals dec-<axis-id> naming the question answered (e.g. dec-oltp-store)."`
	Title        string            `json:"title" jsonschema:"One-line human-readable headline naming the chosen direction (e.g. 'Postgres 16 with PostGIS')."`
	Summary      string            `json:"summary,omitempty" jsonschema:"One-sentence 'what' summary for index/manifest views. Distinct from rationale (the 'why')."`
	Status       string            `json:"status" jsonschema:"Lifecycle status. One of: proposed, assumed, inferred, active. Use 'proposed' for in-flight; 'active' for committed."`
	Confidence   float64           `json:"confidence" jsonschema:"Confidence in this choice on [0, 1]. Use 1.0 for high-confidence commits; lower values flag uncertainty for downstream review."`
	Rationale    string            `json:"rationale" jsonschema:"The 'why' — a complete paragraph citing GOALS clauses, evidence, or constraints that drove the choice."`
	Alternatives []mcpAlternative  `json:"alternatives,omitempty" jsonschema:"Considered but not chosen options, each with name, rationale, rejected_because, and citations backing the rejection."`
	Axes         []string          `json:"axes,omitempty" jsonschema:"Stable slug-IDs of the foundational axes this decision answers (DJ-124). Strongly recommended for queryability; defaults to [the id's axis-suffix] when omitted so convergence-by-construction commits aren't blocked by missing axis enumeration. A revise pass can add axes later."`
	SurfacedBy   []string          `json:"surfaced_by,omitempty" jsonschema:"Spec node ids (goal / feature / strategy) that surfaced the axis this decision answers. Optional — the publisher can backfill from graph topology if missing."`
	InfluencedBy []string          `json:"influenced_by,omitempty" jsonschema:"Optional list of related spec node ids that informed this decision."`
}

// mcpAlternative mirrors spec.Alternative with MCP-SDK-compatible
// jsonschema tags. The agent fills these fields; the handler
// converts to spec.Alternative before Put. We can't reuse
// spec.Alternative directly because its struct tags use the invopop
// jsonschema syntax (key=value), which the MCP SDK's jsonschema-go
// reflector rejects.
type mcpAlternative struct {
	Name            string         `json:"name" jsonschema:"The alternative's name — a concrete product or approach (e.g. 'MySQL' or 'Server-rendered React'). A noun phrase rather than a sentence."`
	Rationale       string         `json:"rationale" jsonschema:"Why this alternative was considered seriously. A complete sentence naming the real advantages it offered over the chosen path."`
	RejectedBecause string         `json:"rejected_because" jsonschema:"The specific reason this alternative lost to the chosen option. A complete sentence pointing at a goal clause / constraint / trade-off. 'Not as good' is not a rejection reason — name the constraint."`
	Citations       []mcpCitation  `json:"citations" jsonschema:"Citations backing the rejected_because reasoning. At least one citation is required so the rejection is grounded evidence rather than fabricated trade-off prose."`
}

// mcpCitation mirrors spec.Citation with MCP-SDK-compatible tags.
type mcpCitation struct {
	Kind      string `json:"kind" jsonschema:"Source category. One of: goals (GOALS.md clause), doc (user-imported document), best_practice (named engineering principle), spec_node (another graph node), scout_brief (fact from a scout agent — Excerpt required), web (URL — Reference is the URL, Excerpt holds the verbatim quote)."`
	Reference string `json:"reference" jsonschema:"Identifier of the source — a filesystem path like 'GOALS.md'; a named principle like '12-factor app: stateless processes'; a spec node id like 'strat-frontend'; or a URL."`
	Span      string `json:"span,omitempty" jsonschema:"Localiser within Reference — a line range like 'lines 12-18'; a section heading like '## In Scope'. Empty for whole-document references."`
	Excerpt   string `json:"excerpt,omitempty" jsonschema:"Verbatim quote from the source being cited. Required for Kind=scout_brief and Kind=web."`
}

// toSpecAlternatives converts the MCP-side shadow type into
// spec.Alternative values for SpecStore.Put. The conversion is
// field-for-field; deliberation-log fields (RejectedAtIteration,
// RejectedByConcernText) are zero-valued since they're populated by
// the council's merge pass, not by the agent calling propose.
func toSpecAlternatives(in []mcpAlternative) []spec.Alternative {
	if len(in) == 0 {
		return nil
	}
	out := make([]spec.Alternative, len(in))
	for i, a := range in {
		out[i] = spec.Alternative{
			Name:            a.Name,
			Rationale:       a.Rationale,
			RejectedBecause: a.RejectedBecause,
			Citations:       toSpecCitations(a.Citations),
		}
	}
	return out
}

func toSpecCitations(in []mcpCitation) []spec.Citation {
	if len(in) == 0 {
		return nil
	}
	out := make([]spec.Citation, len(in))
	for i, c := range in {
		out[i] = spec.Citation{
			Kind:      c.Kind,
			Reference: c.Reference,
			Span:      c.Span,
			Excerpt:   c.Excerpt,
		}
	}
	return out
}

type proposeFeatureInput struct {
	ID                 string   `json:"id" jsonschema:"Feature id with feat- prefix (e.g. feat-realtime-dashboard)."`
	Title              string   `json:"title" jsonschema:"One-line human-readable headline naming the feature (e.g. 'Realtime dashboard with live tile updates')."`
	Summary            string   `json:"summary,omitempty" jsonschema:"One-sentence 'what' summary for index/manifest views."`
	Status             string   `json:"status" jsonschema:"Lifecycle status. One of: proposed, inferred, active, removed."`
	Description        string   `json:"description,omitempty" jsonschema:"Multi-paragraph prose describing the feature's user-facing behavior and bounds."`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty" jsonschema:"Concrete user-observable criteria that determine when the feature is done. Each entry is one criterion."`
	Decisions          []string `json:"decisions" jsonschema:"Decision ids this feature depends on. At least one entry is required; every entry must reference a decision present in the graph."`
	Approaches         []string `json:"approaches,omitempty" jsonschema:"Optional list of approach ids that implement this feature."`
}

type proposeStrategyInput struct {
	ID            string            `json:"id" jsonschema:"Strategy id with strat- prefix (e.g. strat-storage-platform)."`
	Title         string            `json:"title" jsonschema:"One-line human-readable headline naming the strategy."`
	Summary       string            `json:"summary,omitempty" jsonschema:"One-sentence 'what' summary for index/manifest views."`
	Kind          string            `json:"kind" jsonschema:"Strategy kind. One of: foundational, derived, quality. Foundational strategies underpin multiple features; derived strategies specialize foundational ones; quality strategies cover cross-cutting concerns."`
	Status        string            `json:"status" jsonschema:"Status string; conventional values are 'proposed' and 'active'."`
	Decisions     []string          `json:"decisions" jsonschema:"Decision ids this strategy depends on. At least one entry is required; every entry must reference a decision present in the graph."`
	Approaches    []string          `json:"approaches,omitempty" jsonschema:"Optional list of approach ids that implement this strategy."`
	Prerequisites []string          `json:"prerequisites,omitempty" jsonschema:"Optional list of prereqs that must be in place before this strategy can be executed."`
	Commands      map[string]string `json:"commands,omitempty" jsonschema:"Optional named command set for adopters. Keys are command names; values are the command strings."`
	Skills        []string          `json:"skills,omitempty" jsonschema:"Optional list of skills required to execute this strategy."`
	InfluencedBy  []string          `json:"influenced_by,omitempty" jsonschema:"Optional list of related spec node ids that informed this strategy."`
}

// reviseDecisionInput intentionally mirrors proposeDecisionInput. The
// behavioral distinction (preserve created_at on revise; reset both
// on propose) lives in the handler; the schema doesn't carry it
// because timestamps aren't agent-facing fields.
type reviseDecisionInput = proposeDecisionInput

// registerWriteTools wires the four write tools onto the server. Each
// handler opens a SpecStore transaction, validates input via the Put
// type-assert / id-prefix checks, commits, and emits a
// notifications/resources/updated for spec://manifest.
func registerWriteTools(server *mcp.Server, store *agent.SpecStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_decision",
		Description: descSpecProposeDecision,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeDecisionInput) (*mcp.CallToolResult, any, error) {
		body, err := buildDecisionBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindDecision, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed decision %s.", in.ID)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_feature",
		Description: descSpecProposeFeature,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeFeatureInput) (*mcp.CallToolResult, any, error) {
		body, err := buildFeatureBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindFeature, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed feature %s.", in.ID)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_strategy",
		Description: descSpecProposeStrategy,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in proposeStrategyInput) (*mcp.CallToolResult, any, error) {
		body, err := buildStrategyBody(in)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindStrategy, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed strategy %s.", in.ID)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_decision",
		Description: descSpecReviseDecision,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in reviseDecisionInput) (*mcp.CallToolResult, any, error) {
		// Preserve created_at from the existing entry. Lookup via
		// SpecStore.GetSpec returns the body as a json.RawMessage that
		// we don't need to unmarshal — we only need to confirm the entry
		// exists and read its CreatedAt. The simpler path is to look at
		// the typed entry directly; SpecStore doesn't expose that, so we
		// derive it from the manifest plus a body lookup against the
		// settled file. For Phase 1 we accept a small read-amplification:
		// look up via GetSpec, decode just enough to read CreatedAt.
		createdAt, ok := existingDecisionCreatedAt(store, in.ID)
		if !ok {
			return errorResult(fmt.Sprintf("spec_revise_decision: decision %q does not exist; use spec_propose_decision to create it", in.ID)), nil, nil
		}
		body, err := buildDecisionBody(in, createdAt)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindDecision, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Revised decision %s.", in.ID)), nil, nil
	})
}

// commitOne wraps "begin → put → commit" in a single transaction.
// Auto-commit per tool call per Q1=A; the MCP client never sees
// transaction state. Errors at any stage roll back so the store is
// always either pre-call or post-commit state, never half.
func commitOne(store *agent.SpecStore, kind agent.SpecKind, id string, body any) error {
	if err := store.Begin(); err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := store.Put(kind, id, body, agent.OriginProposed); err != nil {
		_ = store.Rollback()
		return err
	}
	if err := store.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// buildDecisionBody assembles a spec.Decision from the input,
// filling server-managed fields. createdAt zero-value means "set to
// now" (propose); non-zero preserves the supplied value (revise).
//
// Axes backfill: per DJ-133 every decision's id equals dec-<axis-id>,
// so an empty Axes input is deterministically inferable from the id.
// We backfill rather than reject so convergence-by-construction
// commits aren't blocked by the agent's failure to re-state what's
// already encoded in the id.
func buildDecisionBody(in proposeDecisionInput, createdAt time.Time) (spec.Decision, error) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	axes := in.Axes
	if len(axes) == 0 && strings.HasPrefix(in.ID, "dec-") {
		axes = []string{strings.TrimPrefix(in.ID, "dec-")}
	}
	return spec.Decision{
		ID:           in.ID,
		Summary:      in.Summary,
		Title:        in.Title,
		Status:       spec.DecisionStatus(in.Status),
		Confidence:   in.Confidence,
		Alternatives: toSpecAlternatives(in.Alternatives),
		Rationale:    in.Rationale,
		Axes:         axes,
		SurfacedBy:   in.SurfacedBy,
		InfluencedBy: in.InfluencedBy,
		CreatedAt:    createdAt,
		UpdatedAt:    now,
	}, nil
}

// buildFeatureBody assembles a spec.Feature. createdAt is always set
// to now for propose; a future spec_revise_feature would preserve it.
func buildFeatureBody(in proposeFeatureInput, createdAt time.Time) (spec.Feature, error) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	return spec.Feature{
		ID:                 in.ID,
		Summary:            in.Summary,
		Title:              in.Title,
		Status:             spec.FeatureStatus(in.Status),
		Description:        in.Description,
		AcceptanceCriteria: in.AcceptanceCriteria,
		Decisions:          in.Decisions,
		Approaches:         in.Approaches,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
	}, nil
}

// buildStrategyBody assembles a spec.Strategy. Strategies don't
// carry created_at / updated_at — the spec.Strategy type omits them.
func buildStrategyBody(in proposeStrategyInput) (spec.Strategy, error) {
	return spec.Strategy{
		ID:            in.ID,
		Summary:       in.Summary,
		Title:         in.Title,
		Kind:          spec.StrategyKind(in.Kind),
		Decisions:     in.Decisions,
		Approaches:    in.Approaches,
		Status:        in.Status,
		Prerequisites: in.Prerequisites,
		Commands:      in.Commands,
		Skills:        in.Skills,
		InfluencedBy:  in.InfluencedBy,
	}, nil
}

// existingDecisionCreatedAt looks up the current created_at on a
// settled or in-flight decision. Returns (time.Time{}, false) when
// the id is unknown — the revise tool surfaces that as a tool-level
// error. Used only by spec_revise_decision; the propose tools always
// set created_at = now.
func existingDecisionCreatedAt(store *agent.SpecStore, id string) (time.Time, bool) {
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return time.Time{}, false
	}
	// Body is the typed spec.Decision returned by SpecStore.GetSpec.
	d, ok := entry.Body.(spec.Decision)
	if !ok {
		return time.Time{}, false
	}
	return d.CreatedAt, true
}

// textResult and errorResult build a *CallToolResult with a single
// text content. Matches the v1 helper shape so handlers stay readable.
func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}
