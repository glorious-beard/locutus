package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/spec"
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
	descSpecProposeDecision = "Propose a new architectural decision OR revise an existing one (upsert semantics on id). Input is the full decision body; the server fills created_at + updated_at = now (use spec_revise_decision instead when you need to preserve the original created_at). The id must use the dec- prefix; per DJ-133 the id equals dec-<axis-id> where <axis-id> names the question being answered (e.g. dec-oltp-store, dec-auth-provider). Optional citation fields (DJ-139): advances lists goal-* ids this decision exists to advance (forward direction — the goals this decision is justified by); respects lists agoal-* ids this decision was checked against and admitted under a carve-out (boundary-navigation direction — the anti-goals it sits next to without violating). Polarity is load-bearing: goal-* ids go in advances; agoal-* ids go in respects. Both are informational links, not structural dependencies. On success the manifest is persisted to .borg/spec/decisions/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecProposeFeature = "Propose a new product feature (upsert semantics on id). Input is the full feature body — the server fills created_at + updated_at = now. The id must use the feat- prefix. decisions is required and lists the decision ids this feature depends on; every entry must reference a decision present in the graph. Optional citation fields (DJ-139): advances lists goal-* ids this feature exists to advance (forward direction); respects lists agoal-* ids this feature was checked against and admitted under a carve-out (boundary-navigation direction). Polarity is load-bearing: goal-* ids go in advances; agoal-* ids go in respects. Both are informational links, not structural dependencies. On success the manifest is persisted to .borg/spec/features/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecProposeStrategy = "Propose a new engineering strategy (upsert semantics on id). Input is the full strategy body. The id must use the strat- prefix. decisions is required and lists the decision ids this strategy depends on; every entry must reference a decision present in the graph. Optional citation fields (DJ-139): advances lists goal-* ids this strategy exists to advance (forward direction); respects lists agoal-* ids this strategy was checked against and admitted under a carve-out (boundary-navigation direction). Polarity is load-bearing: goal-* ids go in advances; agoal-* ids go in respects. Both are informational links, not structural dependencies. On success the manifest is persisted to .borg/spec/strategies/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecReviseDecision = "Revise an existing architectural decision. Same input shape as spec_propose_decision, but the id MUST already exist — the server preserves the original created_at and bumps updated_at to now. Use this when modifying a decision that has been published (versus spec_propose_decision which resets the timestamps). Optional citation fields advances (goal-* ids advanced) and respects (agoal-* ids navigated under a carve-out) — the same polarity rule applies as on propose: goal-* in advances, agoal-* in respects. A revise call replaces these slices wholesale; pass the full updated list, not a delta. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecReviseFeature = "Revise an existing product feature. Same input shape as spec_propose_feature, but the id MUST already exist — the server preserves the original created_at and bumps updated_at to now. Use this to update a feature's description, acceptance criteria, decisions[], or citation fields when downstream decision revisions change the user-visible behavior or constraint set the feature commits to. Optional citation fields advances (goal-* ids advanced) and respects (agoal-* ids navigated under a carve-out) carry the polarity rule from propose: goal-* in advances, agoal-* in respects. A revise call replaces these slices wholesale; pass the full updated list, not a delta. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecReviseStrategy = "Revise an existing engineering strategy. Same input shape as spec_propose_strategy, but the id MUST already exist. Use this to update a strategy's body, decisions[], commands, or citation fields when downstream decision revisions change the technology stack or operational pattern the strategy commits to. spec.Strategy has no created_at/updated_at fields today; this tool exists primarily for symmetry with spec_revise_decision and spec_revise_feature plus to gate the upsert behind an exists-check. Optional citation fields advances (goal-* ids advanced) and respects (agoal-* ids navigated under a carve-out) carry the polarity rule from propose: goal-* in advances, agoal-* in respects. A revise call replaces these slices wholesale; pass the full updated list, not a delta. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."

	// Goal-layer write surface (DJ-139). Goals and AntiGoals are the
	// persisted LLM interpretation of GOALS.md — leaves in the
	// cascade sense; other kinds cite them through informational
	// .advances / .respects fields rather than structural decisions[].
	// The delete tools are the first non-append-only mutations in the
	// spec graph; they exist because GOALS.md edits can drop scope
	// claims and the persisted interpretation has to follow.

	descSpecProposeGoal = "Propose a new goal-* node (upsert semantics on id). Goals are the persisted LLM interpretation of GOALS.md — one atomic in-scope claim per node (DJ-139). Required fields: id (must use the goal- prefix; example: goal-strategic-planning-tool), title (one-line headline), body (the claim's substance), source_clause (the exact phrasing from GOALS.md that this goal interprets — load-bearing for the diff-and-apply sync algorithm that preserves ids across GOALS.md rephrasings). The server fills created_at + updated_at = now; use spec_revise_goal instead when revising an existing goal so created_at is preserved. On success the node lands at .borg/spec/goals/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated. A goal is ANCHORED when you supply source_clause (a verbatim GOALS.md excerpt) and UNANCHORED when you instead supply origin (a provenance note like 'mission statement' or a scope-encoding decision id) for a claim inferred without a verbatim GOALS.md sentence. Supply exactly one. Unanchored goals persist across GOALS.md edits and are never deleted merely for being absent from GOALS.md (DJ-141)."

	descSpecReviseGoal = "Revise an existing goal-* node. Same input shape as spec_propose_goal, but the id MUST already exist — the server preserves the original created_at and bumps updated_at to now. Use this when the LLM's interpretation of a GOALS.md clause shifts (e.g. the source_clause text was rephrased, or the body now reflects a sharper reading of what's in scope). Editing source_clause is the path the diff-and-apply sync uses to track GOALS.md rephrasings without minting new ids. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated. Revising an unanchored node to set source_clause (and clear origin) is the PROMOTED path — it anchors a previously-inferred node when a matching GOALS.md clause appears, preserving the id (DJ-141)."

	descSpecDeleteGoal = "Delete a goal-* node. Removes the entry from the in-memory store AND from the on-disk file .borg/spec/goals/<id>.json. Goal deletion exists because GOALS.md edits can drop scope claims; when the LLM's interpretation of GOALS.md no longer includes a particular goal, the persisted node disappears with it. Required fields: id (goal- prefix; example: goal-strategic-planning-tool) and reason (a complete sentence naming why the node is being deleted; recorded verbatim on the goal_deleted history event as the audit trail that survives the node). Returns a tool-level error when the id is unknown — callers learn that the delete didn't happen rather than treating the no-op as success. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecProposeAntiGoal = "Propose a new agoal-* node (upsert semantics on id). AntiGoals are the persisted LLM interpretation of GOALS.md's out-of-scope claims — one atomic exclusion per node (DJ-139). Required fields: id (must use the agoal- prefix; example: agoal-fundraising), title (one-line headline), body (the exclusion's substance), source_clause (the exact phrasing from GOALS.md that this anti-goal interprets — load-bearing for the diff-and-apply sync algorithm that preserves ids across GOALS.md rephrasings). Optional fields: ceded_to (incumbents owning the ceded space, e.g. ['Carta', 'AngelList'] for a fundraising-tracking exclusion — consumed by the import-conflict-detection playbook), kept_in (carve-out clauses that stay in scope despite the broader exclusion, e.g. ['runway forecasting for product timeline planning'] — consumed by carve-out fit judgment during import). The server fills created_at + updated_at = now; use spec_revise_antigoal to preserve created_at on an existing id. On success the node lands at .borg/spec/antigoals/<id>.json and every subscriber to spec://manifest receives notifications/resources/updated. An anti-goal is ANCHORED when you supply source_clause (a verbatim GOALS.md excerpt) and UNANCHORED when you instead supply origin (a provenance note like 'mission statement' or a scope-encoding decision id) for a claim inferred without a verbatim GOALS.md sentence. Supply exactly one. Unanchored anti-goals persist across GOALS.md edits and are never deleted merely for being absent from GOALS.md (DJ-141)."

	descSpecReviseAntiGoal = "Revise an existing agoal-* node. Same input shape as spec_propose_antigoal, but the id MUST already exist — the server preserves the original created_at and bumps updated_at to now. Use this when the LLM's interpretation of a GOALS.md out-of-scope clause shifts (the source_clause was rephrased, the body sharpens what's excluded, or the ceded_to / kept_in lists change). A revise call replaces ceded_to and kept_in wholesale; pass the full updated lists, not a delta. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated. Revising an unanchored node to set source_clause (and clear origin) is the PROMOTED path — it anchors a previously-inferred node when a matching GOALS.md clause appears, preserving the id (DJ-141)."

	descSpecDeleteAntiGoal = "Delete an agoal-* node. Removes the entry from the in-memory store AND from the on-disk file .borg/spec/antigoals/<id>.json. Anti-goal deletion exists because GOALS.md edits can lift carve-outs; when an out-of-scope claim no longer appears in GOALS.md the persisted node disappears with it. Required fields: id (agoal- prefix; example: agoal-fundraising) and reason (a complete sentence naming why the node is being deleted; recorded verbatim on the antigoal_deleted history event as the audit trail that survives the node). Returns a tool-level error when the id is unknown. On success the manifest is persisted and every subscriber to spec://manifest receives notifications/resources/updated."

	// descSpecMarkApproachDrifted — DJ-138 cascade drift surface.
	// The cascade playbook calls this tool once per approach in the
	// reference-graph closure of a `--with` run. Setting
	// Approach.InvalidatedByEventID flags the approach as stale
	// without re-synthesizing it (that's adopt's role per the
	// layered design). The conservative-closure-mark posture
	// over-marks on rationale-only refreshes; that's accepted
	// because (a) false positives are cheap (a Phase-2 adopt run
	// re-validates), (b) the marks are auditable via the linked
	// approach_drifted history event, and (c) precision improvements
	// (witness-state hashing) are deferred to a follow-up DJ.
	descSpecMarkApproachDrifted = "Mark an Approach as drifted by setting its invalidated_by_event_id field. Use this tool during a `--with` strong-bias cascade to flag every approach in the reference-graph closure (children of rewritten features/strategies plus approaches whose decisions[] cites a flipped decision). The drift mark signals that synthesis is stale; the approach's body is NOT modified by this call — re-synthesis is adopt's responsibility, not the cascade's. Input is {approach_id, event_id} where approach_id must start with app- and resolve to a known approach, and event_id is the id of the originating spec_biased or approach_drifted history event (callers use the spec_biased root event's id). Idempotent on the same (approach_id, event_id) pair. Rejects unknown approach ids, non-Approach kinds, and empty event_id."

	// descSpecUpdateGoalsMdHash — DJ-139 phase 4 short-circuit signal.
	// The `refine goals` playbook (DJ-139 phase 6) calls this tool at
	// the end of a successful goal-layer sync so the next run can
	// compare the persisted hash against hash(GOALS.md_now) and skip
	// step 1 (the LLM-driven diff matcher) when the bytes haven't
	// changed. The handler does a read-modify-write on .borg/manifest.json
	// so every other top-level field is preserved.
	descSpecUpdateGoalsMdHash = "Update .borg/manifest.json's goals_md_hash and goals_md_synced_at fields (DJ-139). The Phase 6 refine-goals playbook calls this tool at the end of a successful goal-layer sync; the next run reads the hash back and short-circuits the matcher when GOALS.md hasn't changed. Input is {hash} where hash is the sha256:<hex> digest of the current GOALS.md bytes (use the canonical spec.ComputeGoalsMdHash helper to produce it). The sync timestamp is stamped server-side from the wall clock — the agent does not supply it. The handler preserves every other manifest field; hash is required and rejected when empty so a typo doesn't accidentally drop the audit-trail timestamp without recording a state change."

	// Approach write surface (DJ-148). Approaches bind features/strategies/bugs
	// to the source files that implement them. Both tools require source_files
	// + source_hash so the resulting approach establishes coherent (spec, code,
	// approach) state. The parent kind is derived from parent_id's prefix.
	descSpecProposeApproach = "Propose a new approach binding a feature/strategy/bug to the source files that implement it (upsert semantics on id). Input is the full approach body — the server fills created_at + updated_at = now (use spec_revise_approach instead when you need to preserve the original created_at). The id must use the app- prefix and conventionally follows app-<parent-id> per DJ-087 (e.g. app-feat-login-flow). parent_id is required and must reference an existing feature (feat-), strategy (strat-), or bug (bug-) — the parent kind is derived from the prefix. source_files is required and lists the relative paths the approach binds to (every path must exist in the working tree at proposal time). source_hash is required and is the sha256:<hex> hash over the sorted-paths-then-contents of source_files, computed by the agent — opaque to the daemon. Optional citation fields (DJ-139): advances lists goal-* ids; respects lists agoal-* ids. On success the manifest is persisted to .borg/spec/approaches/<id>.md (YAML frontmatter + markdown body) and every subscriber to spec://manifest receives notifications/resources/updated."

	descSpecReviseApproach = "Revise an existing approach (the id MUST already exist; use spec_propose_approach instead to create a new one). Input shape is identical to spec_propose_approach. Preserves the original created_at; the server updates updated_at + source_hash_synced_at to now. The parent_id may be changed (e.g. when a feature is reparented under a different strategy) but the new parent must exist and the prefix dictates the new parent kind."
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
	Advances     []string          `json:"advances,omitempty" jsonschema:"Optional list of goal-* ids this decision exists to advance (DJ-139, forward direction). Only goal-* ids belong here; anti-goal navigation goes in respects. Informational — not a structural dependency. Empty / omitted is fine when no goal-layer attribution applies yet."`
	Respects     []string          `json:"respects,omitempty" jsonschema:"Optional list of agoal-* ids this decision was checked against and admitted under a carve-out (DJ-139, boundary-navigation direction). Only agoal-* ids belong here; forward-direction goal references go in advances. Informational — not a structural dependency."`
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
	Advances           []string `json:"advances,omitempty" jsonschema:"Optional list of goal-* ids this feature exists to advance (DJ-139, forward direction). Only goal-* ids belong here; anti-goal navigation goes in respects. Informational — not a structural dependency. Empty / omitted is fine when no goal-layer attribution applies yet."`
	Respects           []string `json:"respects,omitempty" jsonschema:"Optional list of agoal-* ids this feature was checked against and admitted under a carve-out (DJ-139, boundary-navigation direction). Only agoal-* ids belong here; forward-direction goal references go in advances. Informational — not a structural dependency."`
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
	Advances      []string          `json:"advances,omitempty" jsonschema:"Optional list of goal-* ids this strategy exists to advance (DJ-139, forward direction). Only goal-* ids belong here; anti-goal navigation goes in respects. Informational — not a structural dependency. Empty / omitted is fine when no goal-layer attribution applies yet."`
	Respects      []string          `json:"respects,omitempty" jsonschema:"Optional list of agoal-* ids this strategy was checked against and admitted under a carve-out (DJ-139, boundary-navigation direction). Only agoal-* ids belong here; forward-direction goal references go in advances. Informational — not a structural dependency."`
}

// reviseDecisionInput intentionally mirrors proposeDecisionInput. The
// behavioral distinction (preserve created_at on revise; reset both
// on propose) lives in the handler; the schema doesn't carry it
// because timestamps aren't agent-facing fields.
type reviseDecisionInput = proposeDecisionInput

// reviseFeatureInput mirrors proposeFeatureInput; same shape, same
// rationale as reviseDecisionInput. The id-must-exist gate lives in
// the handler.
type reviseFeatureInput = proposeFeatureInput

// reviseStrategyInput mirrors proposeStrategyInput.
type reviseStrategyInput = proposeStrategyInput

// proposeApproachInput shapes the spec_propose_approach tool input.
// Per DJ-148, source_files + source_hash are required so the
// resulting approach establishes coherent (spec, code, approach)
// state. The parent kind is derived from parent_id's prefix
// (feat- / strat- / bug-).
type proposeApproachInput struct {
	ID          string   `json:"id" jsonschema:"Approach id with app- prefix; conventionally follows app-<parent-id> (e.g. app-feat-login-flow, app-strat-cache-layer)."`
	Title       string   `json:"title" jsonschema:"One-line human-readable headline naming the approach (e.g. 'React + Redux for dashboard state management')."`
	Summary     string   `json:"summary,omitempty" jsonschema:"One-sentence summary for index/manifest views."`
	ParentID    string   `json:"parent_id" jsonschema:"Id of the parent node — a feature (feat-), strategy (strat-), or bug (bug-) this approach implements. Required; the parent prefix dictates the parent kind."`
	Body        string   `json:"body" jsonschema:"Multi-paragraph prose describing the implementation approach, architecture rationale, and integration points with the parent."`
	SourceFiles []string `json:"source_files" jsonschema:"List of relative filesystem paths this approach binds to — the source code files implementing it. Required and must be non-empty; every path must exist in the working tree at proposal time."`
	SourceHash  string   `json:"source_hash" jsonschema:"Required sha256:<hex> hash over the sorted-paths-then-contents of source_files (computed by the agent; opaque to the daemon). Establishes coherent (spec, code, approach) state."`
	Decisions   []string `json:"decisions,omitempty" jsonschema:"Optional list of decision ids this approach depends on."`
	Advances    []string `json:"advances,omitempty" jsonschema:"Optional list of goal-* ids this approach exists to advance (DJ-139, forward direction). Only goal-* ids belong here; anti-goal navigation goes in respects. Informational — not a structural dependency."`
	Respects    []string `json:"respects,omitempty" jsonschema:"Optional list of agoal-* ids this approach was checked against and admitted under a carve-out (DJ-139, boundary-navigation direction). Only agoal-* ids belong here; forward-direction goal references go in advances. Informational — not a structural dependency."`
}

// reviseApproachInput is structurally identical to proposeApproachInput;
// the existence check and created_at preservation are handled at the
// handler level, not at the type level.
type reviseApproachInput = proposeApproachInput

// proposeGoalInput shapes spec_propose_goal's payload. Mirrors
// spec.Goal at the agent-facing field level; the handler fills
// CreatedAt + UpdatedAt server-side.
type proposeGoalInput struct {
	ID           string `json:"id" jsonschema:"Goal id with goal- prefix; the slug encodes the in-scope claim (e.g. goal-strategic-planning-tool, goal-multi-tenancy)."`
	Title        string `json:"title" jsonschema:"One-line human-readable headline naming the in-scope claim (e.g. 'Strategic planning tool')."`
	Body         string `json:"body" jsonschema:"The claim's substance — a complete sentence or short paragraph describing what's in scope. The body is what downstream nodes cite when they advance this goal."`
	SourceClause string `json:"source_clause,omitempty" jsonschema:"For an ANCHORED goal: verbatim text from GOALS.md that this goal interprets. Supply exactly one of source_clause or origin. Load-bearing for the diff-and-apply sync — preserves the goal's id across GOALS.md rephrasings by matching against this clause."`
	Origin       string `json:"origin,omitempty" jsonschema:"For an UNANCHORED goal (DJ-141): a short free-text note naming where the claim came from when it has NO verbatim GOALS.md excerpt — e.g. 'mission statement' or a scope-encoding decision id like 'dec-product-scope-boundary'. Supply exactly one of source_clause or origin. Unanchored goals are never deleted merely for being absent from GOALS.md."`
}

// reviseGoalInput mirrors proposeGoalInput; the behavioral
// distinction (preserve created_at, require existing id) lives in
// the handler.
type reviseGoalInput = proposeGoalInput

// deleteGoalInput shapes spec_delete_goal's payload. Narrow by
// design: every delete is one tool call, and the reason field is
// required so the goal_deleted history event records why the node
// disappeared.
type deleteGoalInput struct {
	ID     string `json:"id" jsonschema:"Goal id with goal- prefix. Must resolve to an existing goal in the spec graph."`
	Reason string `json:"reason" jsonschema:"A complete sentence naming why this goal is being deleted (e.g. 'claim dropped from GOALS.md in the 2026-05-27 edit'). Recorded verbatim on the goal_deleted history event so the audit trail survives the node."`
}

// proposeAntiGoalInput shapes spec_propose_antigoal's payload.
// Mirrors spec.AntiGoal — adds CededTo and KeptIn over the Goal
// shape because anti-goals carry the carve-out detail the import-
// conflict-detection playbook reads.
type proposeAntiGoalInput struct {
	ID           string   `json:"id" jsonschema:"AntiGoal id with agoal- prefix; the slug encodes the out-of-scope claim (e.g. agoal-fundraising, agoal-hardware-design)."`
	Title        string   `json:"title" jsonschema:"One-line human-readable headline naming the exclusion (e.g. 'Fundraising tracking')."`
	Body         string   `json:"body" jsonschema:"The exclusion's substance — a complete sentence describing what's out of scope. Consumed by import-conflict-detection when judging whether an incoming feature lands inside the exclusion."`
	SourceClause string   `json:"source_clause,omitempty" jsonschema:"For an ANCHORED anti-goal: verbatim text from GOALS.md that this anti-goal interprets. Supply exactly one of source_clause or origin. Load-bearing for the diff-and-apply sync algorithm — preserves the anti-goal's id across GOALS.md rephrasings."`
	Origin       string   `json:"origin,omitempty" jsonschema:"For an UNANCHORED anti-goal (DJ-141): a short free-text note naming where the exclusion came from when it has NO verbatim GOALS.md excerpt — e.g. 'mission statement' or a scope-encoding decision id like 'dec-product-scope-boundary'. Supply exactly one of source_clause or origin. Unanchored anti-goals are never deleted merely for being absent from GOALS.md."`
	CededTo      []string `json:"ceded_to,omitempty" jsonschema:"Optional list of incumbents owning the ceded space (e.g. ['Carta', 'AngelList'] for a fundraising-tracking exclusion). Consumed by the import-conflict-detection playbook to phrase 'this feature would put us into <incumbent>'s space' reports. Omit when the exclusion is bounded by domain rather than by competitor."`
	KeptIn       []string `json:"kept_in,omitempty" jsonschema:"Optional list of carve-out clauses that stay in scope despite the broader exclusion (e.g. ['runway forecasting for product timeline planning'] within an out-of-scope fundraising claim). Consumed by carve-out fit judgment during import. Omit when the exclusion is total."`
}

// reviseAntiGoalInput mirrors proposeAntiGoalInput; preserve-
// created_at behavior lives in the handler.
type reviseAntiGoalInput = proposeAntiGoalInput

// deleteAntiGoalInput mirrors deleteGoalInput for the agoal-
// surface.
type deleteAntiGoalInput struct {
	ID     string `json:"id" jsonschema:"AntiGoal id with agoal- prefix. Must resolve to an existing antigoal in the spec graph."`
	Reason string `json:"reason" jsonschema:"A complete sentence naming why this antigoal is being deleted (e.g. 'the fundraising carve-out was lifted in the 2026-05-27 GOALS.md edit'). Recorded verbatim on the antigoal_deleted history event."`
}

// markApproachDriftedInput shapes the spec_mark_approach_drifted
// tool's payload. Narrow by design — every drift mark is one tool
// call, surfaced in tools.jsonl so post-mortem session walks see
// the full cascade as a sequence of explicit writes.
type markApproachDriftedInput struct {
	ApproachID string `json:"approach_id" jsonschema:"Approach id with app- prefix. Must resolve to an existing approach in the spec graph."`
	EventID    string `json:"event_id" jsonschema:"Id of the history event that drifted this approach — typically the spec_biased root event id of the current --with run. Stored verbatim on Approach.invalidated_by_event_id."`
}

// updateGoalsMdHashInput shapes the spec_update_goals_md_hash tool's
// payload. The Phase 6 refine-goals playbook calls this once at the
// end of a sync; the handler preserves every other manifest field via
// a read-modify-write through SpecStore.UpdateGoalsMdHash (held under
// the store's write mutex so concurrent clients can't race on the
// non-hash fields of the manifest).
type updateGoalsMdHashInput struct {
	Hash string `json:"hash" jsonschema:"sha256:<hex> digest of the current GOALS.md bytes (use spec.ComputeGoalsMdHash to produce). Required; empty values are rejected. The sync timestamp is recorded server-side from the wall clock — there is no agent-supplied timestamp field."`
}

// registerWriteTools wires the spec_propose_* / spec_revise_* /
// spec_delete_* tools onto the server. Each handler opens a
// SpecStore transaction (or, for delete, calls the dedicated
// DeleteGoal / DeleteAntiGoal methods which are self-contained),
// validates input via the Put type-assert / id-prefix checks,
// persists, and emits a notifications/resources/updated for
// spec://manifest.
//
// hist is the historian used by tools that record audit events
// (today: spec_delete_goal and spec_delete_antigoal). May be nil —
// tools that need history skip event recording when nil so non-
// production wiring (in-memory tests, the daemon's no-historian
// boot path) stays viable.
func registerWriteTools(server *mcp.Server, store *agent.SpecStore, hist *history.Historian) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_decision",
		Description: descSpecProposeDecision,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeDecisionInput) (*mcp.CallToolResult, any, error) {
		body, err := buildDecisionBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindDecision, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed decision %s.", in.ID)), nil, nil
	}, captureProposeDecision(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_feature",
		Description: descSpecProposeFeature,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeFeatureInput) (*mcp.CallToolResult, any, error) {
		body, err := buildFeatureBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindFeature, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed feature %s.", in.ID)), nil, nil
	}, captureProposeFeature(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_strategy",
		Description: descSpecProposeStrategy,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeStrategyInput) (*mcp.CallToolResult, any, error) {
		body, err := buildStrategyBody(in)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindStrategy, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed strategy %s.", in.ID)), nil, nil
	}, captureProposeStrategy(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_decision",
		Description: descSpecReviseDecision,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseDecisionInput) (*mcp.CallToolResult, any, error) {
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
	}, captureReviseDecision(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_feature",
		Description: descSpecReviseFeature,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseFeatureInput) (*mcp.CallToolResult, any, error) {
		createdAt, ok := existingFeatureCreatedAt(store, in.ID)
		if !ok {
			return errorResult(fmt.Sprintf("spec_revise_feature: feature %q does not exist; use spec_propose_feature to create it", in.ID)), nil, nil
		}
		body, err := buildFeatureBody(in, createdAt)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindFeature, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Revised feature %s.", in.ID)), nil, nil
	}, captureReviseFeature(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_strategy",
		Description: descSpecReviseStrategy,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseStrategyInput) (*mcp.CallToolResult, any, error) {
		if !strategyExists(store, in.ID) {
			return errorResult(fmt.Sprintf("spec_revise_strategy: strategy %q does not exist; use spec_propose_strategy to create it", in.ID)), nil, nil
		}
		body, err := buildStrategyBody(in)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindStrategy, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Revised strategy %s.", in.ID)), nil, nil
	}, captureReviseStrategy(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_goal",
		Description: descSpecProposeGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeGoalInput) (*mcp.CallToolResult, any, error) {
		body, err := buildGoalBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindGoal, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed goal %s.", in.ID)), nil, nil
	}, captureProposeGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_goal",
		Description: descSpecReviseGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseGoalInput) (*mcp.CallToolResult, any, error) {
		createdAt, ok := existingGoalCreatedAt(store, in.ID)
		if !ok {
			return errorResult(fmt.Sprintf("spec_revise_goal: goal %q does not exist; use spec_propose_goal to create it", in.ID)), nil, nil
		}
		body, err := buildGoalBody(in, createdAt)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindGoal, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Revised goal %s.", in.ID)), nil, nil
	}, captureReviseGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_delete_goal",
		Description: descSpecDeleteGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in deleteGoalInput) (*mcp.CallToolResult, any, error) {
		id := strings.TrimSpace(in.ID)
		reason := strings.TrimSpace(in.Reason)
		if id == "" {
			return errorResult("spec_delete_goal: id is required"), nil, nil
		}
		if reason == "" {
			return errorResult("spec_delete_goal: reason is required (the audit event captures why the goal was deleted; an empty reason defeats the purpose)"), nil, nil
		}
		if !strings.HasPrefix(id, "goal-") {
			return errorResult(fmt.Sprintf("spec_delete_goal: id %q lacks goal- prefix", id)), nil, nil
		}
		if err := store.DeleteGoal(id); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		// Publish the manifest update before recording history so
		// subscribers see the actual disk state. If history recording
		// fails after the delete is already on disk, the manifest is
		// still correctly reported as updated; the history-write
		// failure is a separate audit-trail concern, not a reason to
		// withhold the manifest notification.
		publishManifestUpdate(ctx, server)
		if hist != nil {
			if err := history.RecordGoalDeleted(hist, id, reason); err != nil {
				// LB-2 (DJ-139 final cross-phase review): the on-disk
				// delete already succeeded, so returning errorResult
				// would lie about disk state — callers retrying on
				// errorResult would hit a "goal not in store" error on
				// the second attempt. Log the audit failure and return
				// success-with-warning so the caller sees what actually
				// landed plus the audit-trail caveat.
				slog.Error("mcp: spec_delete_goal: history record failed after on-disk delete succeeded",
					"id", id, "reason", reason, "error", err)
				return textResult(fmt.Sprintf("Warning: deleted goal %s but failed to record audit event: %v", id, err)), nil, nil
			}
		}
		return textResult(fmt.Sprintf("Deleted goal %s.", id)), nil, nil
	}, captureDeleteGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_propose_antigoal",
		Description: descSpecProposeAntiGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in proposeAntiGoalInput) (*mcp.CallToolResult, any, error) {
		body, err := buildAntiGoalBody(in, time.Time{} /* createdAt = now */)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindAntiGoal, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Proposed antigoal %s.", in.ID)), nil, nil
	}, captureProposeAntiGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_revise_antigoal",
		Description: descSpecReviseAntiGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in reviseAntiGoalInput) (*mcp.CallToolResult, any, error) {
		createdAt, ok := existingAntiGoalCreatedAt(store, in.ID)
		if !ok {
			return errorResult(fmt.Sprintf("spec_revise_antigoal: antigoal %q does not exist; use spec_propose_antigoal to create it", in.ID)), nil, nil
		}
		body, err := buildAntiGoalBody(in, createdAt)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if err := commitOne(store, agent.KindAntiGoal, in.ID, body); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Revised antigoal %s.", in.ID)), nil, nil
	}, captureReviseAntiGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_delete_antigoal",
		Description: descSpecDeleteAntiGoal,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in deleteAntiGoalInput) (*mcp.CallToolResult, any, error) {
		id := strings.TrimSpace(in.ID)
		reason := strings.TrimSpace(in.Reason)
		if id == "" {
			return errorResult("spec_delete_antigoal: id is required"), nil, nil
		}
		if reason == "" {
			return errorResult("spec_delete_antigoal: reason is required (the audit event captures why the antigoal was deleted; an empty reason defeats the purpose)"), nil, nil
		}
		if !strings.HasPrefix(id, "agoal-") {
			return errorResult(fmt.Sprintf("spec_delete_antigoal: id %q lacks agoal- prefix", id)), nil, nil
		}
		if err := store.DeleteAntiGoal(id); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		// Publish the manifest update before recording history (see
		// the matching note in spec_delete_goal): the on-disk delete
		// is the load-bearing state change; the history record is an
		// audit-trail concern that must not gate the manifest signal.
		publishManifestUpdate(ctx, server)
		if hist != nil {
			if err := history.RecordAntiGoalDeleted(hist, id, reason); err != nil {
				// LB-2: same shape as spec_delete_goal — the on-disk
				// delete succeeded, so report success-with-warning
				// rather than errorResult. Returning an error here
				// would misrepresent the disk state and break retry
				// semantics (the next attempt would fail with "agoal
				// not in store").
				slog.Error("mcp: spec_delete_antigoal: history record failed after on-disk delete succeeded",
					"id", id, "reason", reason, "error", err)
				return textResult(fmt.Sprintf("Warning: deleted antigoal %s but failed to record audit event: %v", id, err)), nil, nil
			}
		}
		return textResult(fmt.Sprintf("Deleted antigoal %s.", id)), nil, nil
	}, captureDeleteAntiGoal(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_mark_approach_drifted",
		Description: descSpecMarkApproachDrifted,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in markApproachDriftedInput) (*mcp.CallToolResult, any, error) {
		approachID := strings.TrimSpace(in.ApproachID)
		eventID := strings.TrimSpace(in.EventID)
		if approachID == "" {
			return errorResult("spec_mark_approach_drifted: approach_id is required"), nil, nil
		}
		if eventID == "" {
			return errorResult("spec_mark_approach_drifted: event_id is required (drift marks without an originating event id defeat the audit purpose of invalidated_by_event_id)"), nil, nil
		}
		if !strings.HasPrefix(approachID, "app-") {
			return errorResult(fmt.Sprintf("spec_mark_approach_drifted: %q is not an approach id — only app- prefixed ids are valid (decisions / features / strategies are not drift-mark targets)", approachID)), nil, nil
		}
		res := store.GetSpec([]string{approachID})
		entry, ok := res.Results[approachID]
		if !ok || entry.Status == agent.SpecGetMissing {
			return errorResult(fmt.Sprintf("spec_mark_approach_drifted: approach %q does not exist", approachID)), nil, nil
		}
		approach, ok := entry.Body.(spec.Approach)
		if !ok {
			return errorResult(fmt.Sprintf("spec_mark_approach_drifted: %q resolved to %T, not spec.Approach", approachID, entry.Body)), nil, nil
		}
		approach.InvalidatedByEventID = eventID
		approach.UpdatedAt = time.Now().UTC()
		if err := commitOne(store, agent.KindApproach, approachID, approach); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		publishManifestUpdate(ctx, server)
		return textResult(fmt.Sprintf("Marked approach %s drifted by event %s.", approachID, eventID)), nil, nil
	}, captureMarkApproachDrifted(store)))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_update_goals_md_hash",
		Description: descSpecUpdateGoalsMdHash,
	}, captureOnly(func(ctx context.Context, _ *mcp.CallToolRequest, in updateGoalsMdHashInput) (*mcp.CallToolResult, any, error) {
		hash := strings.TrimSpace(in.Hash)
		if hash == "" {
			return errorResult("spec_update_goals_md_hash: hash is required"), nil, nil
		}
		if err := store.UpdateGoalsMdHash(hash, time.Now().UTC()); err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(fmt.Sprintf("Updated goals_md_hash to %s.", hash)), nil, nil
	}, captureUpdateGoalsMdHash(store)))
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
		Advances:     in.Advances,
		Respects:     in.Respects,
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
		Advances:           in.Advances,
		Respects:           in.Respects,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
	}, nil
}

// buildApproachBody assembles a spec.Approach from the input,
// filling server-managed fields. createdAt zero-value means "set
// to now" (propose); non-zero preserves the supplied value
// (revise). SourceHashSyncedAt is always stamped to now since the
// hash is being recorded.
//
// Parent kind is derived from parent_id's prefix (feat- / strat- /
// bug-). Existence validation happens at the handler level; the
// builder only enforces shape.
func buildApproachBody(in proposeApproachInput, createdAt time.Time) (spec.Approach, error) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return spec.Approach{}, fmt.Errorf("id is required")
	}
	if !strings.HasPrefix(id, "app-") {
		return spec.Approach{}, fmt.Errorf("id %q must use the app- prefix per DJ-087's id convention", id)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return spec.Approach{}, fmt.Errorf("title is required")
	}
	parentID := strings.TrimSpace(in.ParentID)
	if parentID == "" {
		return spec.Approach{}, fmt.Errorf("parent_id is required (the feature, strategy, or bug this approach implements)")
	}
	switch {
	case strings.HasPrefix(parentID, "feat-"),
		strings.HasPrefix(parentID, "strat-"),
		strings.HasPrefix(parentID, "bug-"):
		// ok
	default:
		return spec.Approach{}, fmt.Errorf("parent_id must be a feature (feat-), strategy (strat-), or bug (bug-) id; got %q", parentID)
	}
	if len(in.SourceFiles) == 0 {
		return spec.Approach{}, fmt.Errorf("source_files is required and must list at least one path the approach binds to")
	}
	hash := strings.TrimSpace(in.SourceHash)
	if hash == "" {
		return spec.Approach{}, fmt.Errorf("source_hash is required (compute the sha256:<hex> of sorted-paths-then-contents of source_files)")
	}
	if !strings.HasPrefix(hash, "sha256:") || len(hash) <= len("sha256:") {
		return spec.Approach{}, fmt.Errorf("source_hash must be in sha256:<hex> form; got %q", hash)
	}
	return spec.Approach{
		ID:                 id,
		Title:              title,
		Summary:            strings.TrimSpace(in.Summary),
		ParentID:           parentID,
		Body:               in.Body,
		SourceFiles:        in.SourceFiles,
		SourceHash:         hash,
		SourceHashSyncedAt: now,
		Decisions:          in.Decisions,
		Advances:           in.Advances,
		Respects:           in.Respects,
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
		Advances:      in.Advances,
		Respects:      in.Respects,
	}, nil
}

// validateProvenance enforces DJ-141's exactly-one-of rule: a goal-
// layer node is either anchored (source_clause set, a verbatim GOALS.md
// excerpt) or unanchored (origin set, a provenance note for an inferred
// claim) — never both, never neither.
func validateProvenance(sourceClause, origin string) error {
	hasClause := strings.TrimSpace(sourceClause) != ""
	hasOrigin := strings.TrimSpace(origin) != ""
	switch {
	case hasClause && hasOrigin:
		return fmt.Errorf("supply exactly one of source_clause or origin, not both (an anchored node has source_clause; an unanchored node has origin)")
	case !hasClause && !hasOrigin:
		return fmt.Errorf("supply exactly one of source_clause or origin: set source_clause for a verbatim GOALS.md claim, or origin (e.g. 'mission statement') for an inferred claim")
	}
	return nil
}

// buildGoalBody assembles a spec.Goal from the input. createdAt
// zero-value means "set to now" (propose); non-zero preserves the
// supplied value (revise).
func buildGoalBody(in proposeGoalInput, createdAt time.Time) (spec.Goal, error) {
	if err := validateProvenance(in.SourceClause, in.Origin); err != nil {
		return spec.Goal{}, err
	}
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	return spec.Goal{
		ID:           in.ID,
		Title:        in.Title,
		Body:         in.Body,
		SourceClause: in.SourceClause,
		Origin:       in.Origin,
		CreatedAt:    createdAt,
		UpdatedAt:    now,
	}, nil
}

// buildAntiGoalBody assembles a spec.AntiGoal. Same createdAt
// semantics as buildGoalBody. CededTo and KeptIn are passed through
// verbatim — nil-vs-empty distinction matters for the omitempty JSON
// encoding, so we don't coerce to empty.
func buildAntiGoalBody(in proposeAntiGoalInput, createdAt time.Time) (spec.AntiGoal, error) {
	if err := validateProvenance(in.SourceClause, in.Origin); err != nil {
		return spec.AntiGoal{}, err
	}
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	return spec.AntiGoal{
		ID:           in.ID,
		Title:        in.Title,
		Body:         in.Body,
		SourceClause: in.SourceClause,
		Origin:       in.Origin,
		CededTo:      in.CededTo,
		KeptIn:       in.KeptIn,
		CreatedAt:    createdAt,
		UpdatedAt:    now,
	}, nil
}

// existingGoalCreatedAt looks up the current created_at on a
// settled or in-flight goal. Returns (time.Time{}, false) when the
// id is unknown — the revise tool surfaces that as a tool-level
// error.
func existingGoalCreatedAt(store *agent.SpecStore, id string) (time.Time, bool) {
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return time.Time{}, false
	}
	g, ok := entry.Body.(spec.Goal)
	if !ok {
		return time.Time{}, false
	}
	return g.CreatedAt, true
}

// existingAntiGoalCreatedAt mirrors existingGoalCreatedAt for the
// agoal- surface.
func existingAntiGoalCreatedAt(store *agent.SpecStore, id string) (time.Time, bool) {
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return time.Time{}, false
	}
	ag, ok := entry.Body.(spec.AntiGoal)
	if !ok {
		return time.Time{}, false
	}
	return ag.CreatedAt, true
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

// existingFeatureCreatedAt mirrors existingDecisionCreatedAt for the
// spec_revise_feature path.
func existingFeatureCreatedAt(store *agent.SpecStore, id string) (time.Time, bool) {
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return time.Time{}, false
	}
	f, ok := entry.Body.(spec.Feature)
	if !ok {
		return time.Time{}, false
	}
	return f.CreatedAt, true
}

// strategyExists is the existence-check used by spec_revise_strategy.
// spec.Strategy has no created_at/updated_at fields today, so the
// revise path doesn't need to recover a timestamp the way decisions
// and features do — it only needs to gate the upsert behind an
// "id must already exist" check.
func strategyExists(store *agent.SpecStore, id string) bool {
	res := store.GetSpec([]string{id})
	entry, ok := res.Results[id]
	if !ok || entry.Status == agent.SpecGetMissing {
		return false
	}
	_, ok = entry.Body.(spec.Strategy)
	return ok
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

// ---------------------------------------------------------------
// DJ-147 Task 5 — capture closures for the mutation tools.
//
// Each closure mirrors the corresponding production handler's input
// validation and body construction; instead of commitOne (which writes
// to the base store + disk and fires manifest notifications) it calls
// store.OverlayPut / OverlayDelete / OverlaySetGoalsMdHash. The
// captureOnly wrapper routes to the closure only when the calling
// session is in dry-run mode; outside dry-run the production handler
// runs verbatim.
//
// Important contract: capture closures MUST NOT call
// publishManifestUpdate or hist.Append — dry-run sessions emit no
// manifest notifications and write no audit events. The capture and
// the report (Task 7) are the only operator-visible surface.
//
// createdAt preservation on revise: capture consults the overlay-view
// first (so a revise after a propose in the same session sees the
// would-be entry's CreatedAt), falling back to the base store. The
// FromView helpers are parallel to the existing existing*CreatedAt
// disk-only helpers because the production path doesn't need overlay
// awareness — only the capture path does.
// ---------------------------------------------------------------

func captureProposeDecision(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeDecisionInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeDecisionInput) (any, error) {
		body, err := buildDecisionBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_decision", agent.KindDecision, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureReviseDecision(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseDecisionInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseDecisionInput) (any, error) {
		createdAt, ok := overlayDecisionCreatedAt(store, sess, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_decision: decision %q does not exist; use spec_propose_decision to create it", in.ID)
		}
		body, err := buildDecisionBody(in, createdAt)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_decision", agent.KindDecision, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureProposeFeature(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeFeatureInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeFeatureInput) (any, error) {
		body, err := buildFeatureBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_feature", agent.KindFeature, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureReviseFeature(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseFeatureInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseFeatureInput) (any, error) {
		createdAt, ok := overlayFeatureCreatedAt(store, sess, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_feature: feature %q does not exist; use spec_propose_feature to create it", in.ID)
		}
		body, err := buildFeatureBody(in, createdAt)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_feature", agent.KindFeature, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureProposeStrategy(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeStrategyInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeStrategyInput) (any, error) {
		body, err := buildStrategyBody(in)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_strategy", agent.KindStrategy, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureReviseStrategy(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseStrategyInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseStrategyInput) (any, error) {
		// spec.Strategy has no created_at/updated_at, so the revise
		// path only needs the existence-check (same as the production
		// strategyExists). Consult the overlay-view so a revise after
		// a propose in the same session resolves.
		view := store.OverlayView(sess)
		if _, ok := view.Lookup(agent.KindStrategy, in.ID); !ok {
			return nil, fmt.Errorf("spec_revise_strategy: strategy %q does not exist; use spec_propose_strategy to create it", in.ID)
		}
		body, err := buildStrategyBody(in)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_strategy", agent.KindStrategy, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureProposeGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeGoalInput) (any, error) {
		body, err := buildGoalBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_goal", agent.KindGoal, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureReviseGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseGoalInput) (any, error) {
		createdAt, ok := overlayGoalCreatedAt(store, sess, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_goal: goal %q does not exist; use spec_propose_goal to create it", in.ID)
		}
		body, err := buildGoalBody(in, createdAt)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_goal", agent.KindGoal, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureDeleteGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in deleteGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in deleteGoalInput) (any, error) {
		id := strings.TrimSpace(in.ID)
		reason := strings.TrimSpace(in.Reason)
		if id == "" {
			return nil, fmt.Errorf("spec_delete_goal: id is required")
		}
		if reason == "" {
			return nil, fmt.Errorf("spec_delete_goal: reason is required (the audit event captures why the goal was deleted; an empty reason defeats the purpose)")
		}
		if !strings.HasPrefix(id, "goal-") {
			return nil, fmt.Errorf("spec_delete_goal: id %q lacks goal- prefix", id)
		}
		// Existence check against the overlay-view so a delete after a
		// propose in the same session resolves.
		view := store.OverlayView(sess)
		if _, ok := view.Lookup(agent.KindGoal, id); !ok {
			return nil, fmt.Errorf("spec_delete_goal: goal %q does not exist", id)
		}
		if err := store.OverlayDelete(sess, "spec_delete_goal", agent.KindGoal, id); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func captureProposeAntiGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in proposeAntiGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in proposeAntiGoalInput) (any, error) {
		body, err := buildAntiGoalBody(in, time.Time{})
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_propose_antigoal", agent.KindAntiGoal, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureReviseAntiGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in reviseAntiGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in reviseAntiGoalInput) (any, error) {
		createdAt, ok := overlayAntiGoalCreatedAt(store, sess, in.ID)
		if !ok {
			return nil, fmt.Errorf("spec_revise_antigoal: antigoal %q does not exist; use spec_propose_antigoal to create it", in.ID)
		}
		body, err := buildAntiGoalBody(in, createdAt)
		if err != nil {
			return nil, err
		}
		if err := store.OverlayPut(sess, "spec_revise_antigoal", agent.KindAntiGoal, in.ID, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func captureDeleteAntiGoal(store *agent.SpecStore) func(sess *mcp.ServerSession, in deleteAntiGoalInput) (any, error) {
	return func(sess *mcp.ServerSession, in deleteAntiGoalInput) (any, error) {
		id := strings.TrimSpace(in.ID)
		reason := strings.TrimSpace(in.Reason)
		if id == "" {
			return nil, fmt.Errorf("spec_delete_antigoal: id is required")
		}
		if reason == "" {
			return nil, fmt.Errorf("spec_delete_antigoal: reason is required (the audit event captures why the antigoal was deleted; an empty reason defeats the purpose)")
		}
		if !strings.HasPrefix(id, "agoal-") {
			return nil, fmt.Errorf("spec_delete_antigoal: id %q lacks agoal- prefix", id)
		}
		view := store.OverlayView(sess)
		if _, ok := view.Lookup(agent.KindAntiGoal, id); !ok {
			return nil, fmt.Errorf("spec_delete_antigoal: antigoal %q does not exist", id)
		}
		if err := store.OverlayDelete(sess, "spec_delete_antigoal", agent.KindAntiGoal, id); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func captureMarkApproachDrifted(store *agent.SpecStore) func(sess *mcp.ServerSession, in markApproachDriftedInput) (any, error) {
	return func(sess *mcp.ServerSession, in markApproachDriftedInput) (any, error) {
		approachID := strings.TrimSpace(in.ApproachID)
		eventID := strings.TrimSpace(in.EventID)
		if approachID == "" {
			return nil, fmt.Errorf("spec_mark_approach_drifted: approach_id is required")
		}
		if eventID == "" {
			return nil, fmt.Errorf("spec_mark_approach_drifted: event_id is required (drift marks without an originating event id defeat the audit purpose of invalidated_by_event_id)")
		}
		if !strings.HasPrefix(approachID, "app-") {
			return nil, fmt.Errorf("spec_mark_approach_drifted: %q is not an approach id — only app- prefixed ids are valid (decisions / features / strategies are not drift-mark targets)", approachID)
		}
		view := store.OverlayView(sess)
		entry, ok := view.Lookup(agent.KindApproach, approachID)
		if !ok {
			return nil, fmt.Errorf("spec_mark_approach_drifted: approach %q does not exist", approachID)
		}
		approach, ok := entry.Body.(spec.Approach)
		if !ok {
			return nil, fmt.Errorf("spec_mark_approach_drifted: %q resolved to %T, not spec.Approach", approachID, entry.Body)
		}
		approach.InvalidatedByEventID = eventID
		approach.UpdatedAt = time.Now().UTC()
		if err := store.OverlayPut(sess, "spec_mark_approach_drifted", agent.KindApproach, approachID, approach); err != nil {
			return nil, err
		}
		return approach, nil
	}
}

func captureUpdateGoalsMdHash(store *agent.SpecStore) func(sess *mcp.ServerSession, in updateGoalsMdHashInput) (any, error) {
	return func(sess *mcp.ServerSession, in updateGoalsMdHashInput) (any, error) {
		hash := strings.TrimSpace(in.Hash)
		if hash == "" {
			return nil, fmt.Errorf("spec_update_goals_md_hash: hash is required")
		}
		syncedAt := time.Now().UTC()
		if err := store.OverlaySetGoalsMdHash(sess, hash, syncedAt); err != nil {
			return nil, err
		}
		return map[string]any{"hash": hash, "synced_at": syncedAt}, nil
	}
}

// overlayDecisionCreatedAt looks up the current created_at on a
// decision via the session's overlay-view: a would-be entry written
// earlier in the same dry-run session wins; otherwise the base store.
// Returns (time.Time{}, false) when the id is unknown.
func overlayDecisionCreatedAt(store *agent.SpecStore, sess *mcp.ServerSession, id string) (time.Time, bool) {
	view := store.OverlayView(sess)
	entry, ok := view.Lookup(agent.KindDecision, id)
	if !ok {
		return time.Time{}, false
	}
	d, ok := entry.Body.(spec.Decision)
	if !ok {
		return time.Time{}, false
	}
	return d.CreatedAt, true
}

func overlayFeatureCreatedAt(store *agent.SpecStore, sess *mcp.ServerSession, id string) (time.Time, bool) {
	view := store.OverlayView(sess)
	entry, ok := view.Lookup(agent.KindFeature, id)
	if !ok {
		return time.Time{}, false
	}
	f, ok := entry.Body.(spec.Feature)
	if !ok {
		return time.Time{}, false
	}
	return f.CreatedAt, true
}

func overlayGoalCreatedAt(store *agent.SpecStore, sess *mcp.ServerSession, id string) (time.Time, bool) {
	view := store.OverlayView(sess)
	entry, ok := view.Lookup(agent.KindGoal, id)
	if !ok {
		return time.Time{}, false
	}
	g, ok := entry.Body.(spec.Goal)
	if !ok {
		return time.Time{}, false
	}
	return g.CreatedAt, true
}

func overlayAntiGoalCreatedAt(store *agent.SpecStore, sess *mcp.ServerSession, id string) (time.Time, bool) {
	view := store.OverlayView(sess)
	entry, ok := view.Lookup(agent.KindAntiGoal, id)
	if !ok {
		return time.Time{}, false
	}
	ag, ok := entry.Body.(spec.AntiGoal)
	if !ok {
		return time.Time{}, false
	}
	return ag.CreatedAt, true
}
