package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// FileEntry represents a file in the codebase inventory.
type FileEntry struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// AssimilationRequest holds inputs for the assimilation analysis pipeline.
// ExistingSpec, when non-nil, is surfaced to the scout agent as "here is
// what the spec already covers" context so the LLM can distinguish new
// nodes from enhancements of existing ones per the Round-1 ambiguity 2
// resolution. Leaving ExistingSpec nil preserves the greenfield shape.
type AssimilationRequest struct {
	Inventory    []FileEntry
	ExistingSpec *ExistingSpec

	// Sink, when non-nil, receives a WorkflowEvent for every agent
	// step in the assimilation council. Same contract as
	// SpecGenRequest.Sink — drives the CLI spinner UI and MCP
	// progress notifications. Nil sink is silent.
	Sink EventSink
}

// ExistingSpec is a snapshot of what the spec store already contains. The
// assimilation pipeline reads this before inference and includes it in
// the scout prompt so the LLM can output updates to existing nodes
// (matching IDs) as well as new nodes.
type ExistingSpec struct {
	Features   []spec.Feature
	Decisions  []spec.Decision
	Strategies []spec.Strategy
	Approaches []spec.Approach
	// Entities is populated by the caller from a prior assimilation run's
	// in-memory output when available. It is NOT loaded from `.borg/spec/`
	// (no such directory exists per DJ-076) — if the caller doesn't have
	// an entity projection handy, this stays nil and the LLM reconstructs
	// from code on the current run.
	Entities []spec.Entity
}

// IsEmpty reports whether the snapshot has any nodes at all. Greenfield
// runs will find an empty snapshot on first invocation; the pipeline
// should still report that truthfully rather than fabricate a non-empty
// context.
func (e *ExistingSpec) IsEmpty() bool {
	if e == nil {
		return true
	}
	return len(e.Features)+len(e.Decisions)+len(e.Strategies)+len(e.Approaches)+len(e.Entities) == 0
}

// AssimilationResult holds the full output of assimilation analysis.
// Entities are included as in-memory context per DJ-076 — downstream
// agents (planner, supervisor, remediator) consume them for structured
// domain knowledge, but the persistence layer does NOT write them to
// `.borg/spec/entities/`. The code (Go structs, migrations, proto
// files) remains the authoritative schema; Entities here are a working
// projection of that code for LLM context during the same run.
type AssimilationResult struct {
	Features   []spec.Feature
	Decisions  []spec.Decision
	Strategies []spec.Strategy
	Approaches []spec.Approach
	Entities   []spec.Entity
	Gaps       []Gap
}

// Gap represents a detected gap in the codebase.
type Gap struct {
	Category    string   `json:"category" jsonschema:"description=Short kebab-case classifier for the gap kind — e.g. missing_tests; orphan_code; undocumented_decision; missing_quality_strategy. Drives downstream routing; the remediator inspects this to pick which feature/strategy slot to populate."`
	Severity    string   `json:"severity" jsonschema:"enum=high,enum=medium,enum=low,description=high = will cause production issues / security gaps / onboarding friction; medium = creates technical debt or friction; low = nice-to-have polish."`
	Description string   `json:"description" jsonschema:"description=One-to-three-sentence description of the gap. Concrete enough that the remediator can author a remediation step without needing additional context — names the specific file/decision/entity and what's missing about it."`
	AffectedIDs []string `json:"affected_ids,omitempty" jsonschema:"description=Spec node ids (feature / decision / strategy / entity) the gap relates to. Empty when the gap is cross-cutting or doesn't map onto a specific existing node."`
}

// AssimilationContribution is the per-call output of every agent in
// the assimilation pipeline (scout / backend_analyzer / frontend_analyzer
// / infra_analyzer / gap_analyst). It's a union of contribution slots —
// each agent populates the subset that fits its task. The orchestrator's
// parseAssimilationResults merges contributions across all rounds into
// a single AssimilationResult.
//
// Historically this contract was implicit: agents emitted free-form JSON
// and the parser pulled out a fixed set of known field names via a
// loose `map[string]json.RawMessage` walk. Typing the contract here
// gives the dispatcher a real schema to enforce strict-mode against
// (per-provider structured-output API) so the agents' .md frontmatter
// `output_schema:` references resolve to something concrete.
type AssimilationContribution struct {
	Features   []spec.Feature  `json:"features,omitempty" jsonschema:"description=Feature nodes the agent inferred from the codebase. Each is a user-facing capability the project delivers; the analyzer surfaces these from code structure / framework patterns / module boundaries."`
	Decisions  []spec.Decision `json:"decisions,omitempty" jsonschema:"description=Architectural decisions the agent inferred from concrete evidence in the codebase. Each names a committed choice (e.g. 'Use PostgreSQL for OLTP store') with rationale grounded in observable file evidence — go.mod entries; framework configs; CI commands."`
	Strategies []spec.Strategy `json:"strategies,omitempty" jsonschema:"description=Cross-cutting engineering commitments the agent surfaced — testing approach; deployment posture; observability stack; build system. Strategies bundle related decisions and the prerequisites/commands that operationalize them."`
	Approaches []spec.Approach `json:"approaches,omitempty" jsonschema:"description=Implementation approaches the agent surfaced — typically empty for analyzer agents; populated by adopt-time synthesis. Listed here for completeness since the parser accepts the field."`
	Entities   []spec.Entity   `json:"entities,omitempty" jsonschema:"description=Domain entities the agent extracted from data models / DB schemas / struct definitions. Each entity carries its fields and relationships so downstream remediation can scope tests and ownership to specific business objects."`
	Gaps       []Gap           `json:"gaps,omitempty" jsonschema:"description=Detected gaps — typically populated only by gap_analyst. Each gap names a missing test / undocumented decision / orphan code / missing quality strategy with a severity tier and the spec node ids it affects."`
}

func init() {
	RegisterSchema("AssimilationContribution", AssimilationContribution{
		Features: []spec.Feature{{
			ID:          "feat-dashboard",
			Title:       "Real-time fleet dashboard",
			Description: "Operators view live fleet status from a single dashboard with sub-second refresh.",
			Status:      spec.FeatureStatusProposed,
		}},
		Decisions: []spec.Decision{{
			ID:         "dec-postgres-oltp",
			Title:      "Use PostgreSQL 16 for OLTP store",
			Rationale:  "Strong relational guarantees; PostGIS available for geospatial queries; team has operational experience.",
			Confidence: 0.85,
			Status:     spec.DecisionStatusProposed,
		}},
		Strategies: []spec.Strategy{{
			ID:    "strat-test-runner",
			Title: "Go test runner with table-driven tests",
			Kind:  spec.StrategyKindQuality,
		}},
		Entities: []spec.Entity{{
			ID:     "e-user",
			Name:   "User",
			Source: "internal/model/user.go",
		}},
		Gaps: []Gap{{
			Category:    "missing_tests",
			Severity:    "high",
			Description: "internal/auth/handler.go has no corresponding handler_test.go; auth flow lacks coverage.",
			AffectedIDs: []string{"e-user", "dec-postgres-oltp"},
		}},
	})
}

// WalkInventory produces a file inventory from the given FS, respecting .gitignore.
func WalkInventory(fsys specio.FS) ([]FileEntry, error) {
	// readOnlyFS (used by --dry-run) wraps an underlying MemFS/OSFS; unwrap
	// so the type switch below recognises the concrete type. Writes through
	// the wrapper are still dropped upstream.
	if u, ok := fsys.(interface{ Unwrap() specio.FS }); ok {
		fsys = u.Unwrap()
	}

	// Collect all file paths.
	var allFiles []string
	if mfs, ok := fsys.(*specio.MemFS); ok {
		allFiles = mfs.AllFiles()
	} else if osfs, ok := fsys.(*specio.OSFS); ok {
		err := filepath.WalkDir(osfs.Base(), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == ".borg") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(osfs.Base(), p)
				allFiles = append(allFiles, rel)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking directory: %w", err)
		}
	} else {
		return nil, fmt.Errorf("WalkInventory: unsupported FS type %T", fsys)
	}

	if len(allFiles) == 0 {
		return nil, nil
	}

	// Parse .gitignore patterns.
	ignorePatterns := parseGitignore(fsys)

	var entries []FileEntry
	for _, fp := range allFiles {
		if isIgnored(fp, ignorePatterns) {
			continue
		}

		info, err := fsys.Stat(fp)
		if err != nil {
			continue
		}

		entries = append(entries, FileEntry{
			Path:  fp,
			Size:  info.Size(),
			IsDir: info.IsDir(),
		})
	}

	return entries, nil
}

// gitignorePattern represents a single parsed .gitignore pattern.
type gitignorePattern struct {
	pattern string
	isDir   bool // pattern ends with "/"
}

// parseGitignore reads .gitignore from the FS and returns parsed patterns.
func parseGitignore(fsys specio.FS) []gitignorePattern {
	data, err := fsys.ReadFile(".gitignore")
	if err != nil {
		return nil
	}

	var patterns []gitignorePattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := gitignorePattern{pattern: line}
		if strings.HasSuffix(line, "/") {
			p.isDir = true
			p.pattern = strings.TrimSuffix(line, "/")
		}
		patterns = append(patterns, p)
	}
	return patterns
}

// isIgnored checks whether a file path matches any gitignore pattern.
func isIgnored(filePath string, patterns []gitignorePattern) bool {
	for _, p := range patterns {
		if p.isDir {
			// Directory pattern: match if path starts with the dir prefix.
			prefix := p.pattern + "/"
			if strings.HasPrefix(filePath, prefix) || filePath == p.pattern {
				return true
			}
		} else {
			// File pattern: match exact path or base name.
			if filePath == p.pattern || path.Base(filePath) == p.pattern {
				return true
			}
		}
	}
	return false
}

// Analyze runs the full assimilation pipeline using the assimilation council workflow.
func Analyze(ctx context.Context, exec AgentExecutor, fsys specio.FS, req AssimilationRequest) (*AssimilationResult, error) {
	const agentsDir = ".borg/agents"

	// Load agent definitions.
	agentList, err := LoadAgentDefs(fsys, agentsDir)
	if err != nil {
		return nil, fmt.Errorf("loading assimilation agents: %w", err)
	}

	agentDefs := make(map[string]AgentDef, len(agentList))
	for _, a := range agentList {
		agentDefs[a.ID] = a
	}

	// Build the initial prompt with inventory context and, when present,
	// a summary of what the spec already covers so the pipeline can emit
	// updates (matching IDs) alongside new nodes.
	inventoryJSON, err := json.Marshal(req.Inventory)
	if err != nil {
		return nil, fmt.Errorf("marshaling inventory: %w", err)
	}

	var promptBuilder strings.Builder
	promptBuilder.WriteString("Analyze this codebase. File inventory:\n")
	promptBuilder.Write(inventoryJSON)
	promptBuilder.WriteString("\n")

	// Existing-spec context is delivered via the spec_list_manifest /
	// spec_get tools (DJ-094, DJ-115), not inlined. The flag below is
	// a data-state hint, not a directive — the scout agent's prompt
	// covers tool usage. On greenfield runs (empty existing spec) we
	// omit the flag entirely so the agent doesn't burn turns on
	// lookups that would return empty.
	//
	// Entities are NOT exposed via the tools (per DJ-076, entities
	// are context carriers, not persisted spec nodes). The scout's
	// prompt is the only path they thread through; when the caller
	// supplies an in-memory entities slice we still inline those
	// since the tool layer has nothing to return for them.
	if !req.ExistingSpec.IsEmpty() {
		promptBuilder.WriteString("\n## Existing spec is present\n\nA persisted spec snapshot exists at `.borg/spec/`; the `spec_list_manifest` and `spec_get` tools will return non-empty results. Call `spec_list_manifest` first to scan ids + summaries; call `spec_get(id)` only for nodes whose detail you need. Update nodes in place (match IDs) rather than duplicating them; emit new nodes only for genuinely new concepts. (On greenfield runs this section is omitted.)\n")
		if len(req.ExistingSpec.Entities) > 0 {
			promptBuilder.WriteString("\n## In-memory entities (not on disk; see DJ-076)\n\n")
			for _, e := range req.ExistingSpec.Entities {
				fmt.Fprintf(&promptBuilder, "- entity %s: %s\n", e.ID, e.Name)
			}
		}
	}

	prompt := promptBuilder.String()

	// Execute the workflow.
	wfExec := &WorkflowExecutor[PlanningState]{
		Executor:  exec,
		AgentDefs: agentDefs,
		Workflow:  AssimilationWorkflow,
	}

	// Bridge workflow events to the caller's sink. Same shape as the
	// spec-generation council — see GenerateSpec for the rationale on
	// buffer sizing. Sink lifecycle is owned by the cmd-layer caller;
	// Analyze drains the bridge channel but leaves the sink open so
	// post-Analyze direct calls (the remediator pass) can still
	// render through it.
	sink := req.Sink
	if sink == nil {
		sink = SilentSink{}
	}
	events := make(chan WorkflowEvent, 64)
	wfExec.Events = events
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		for ev := range events {
			sink.OnEvent(ev)
		}
	}()
	defer func() {
		close(events)
		<-bridgeDone
	}()

	state := &PlanningState{Prompt: prompt, Round: 1}
	results, err := RunCouncil(ctx, wfExec, state)
	if err != nil {
		return nil, fmt.Errorf("assimilation workflow execution: %w", err)
	}

	// Parse results into AssimilationResult.
	return parseAssimilationResults(results)
}

// parseAssimilationResults aggregates all round results into a single AssimilationResult.
func parseAssimilationResults(results []RoundResult) (*AssimilationResult, error) {
	br := &AssimilationResult{}

	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}

		// Try to parse the output as JSON and extract known fields.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.Output), &raw); err != nil {
			// Not JSON — skip.
			continue
		}

		if data, ok := raw["features"]; ok {
			var features []spec.Feature
			if err := json.Unmarshal(data, &features); err == nil {
				br.Features = append(br.Features, features...)
			}
		}

		if data, ok := raw["decisions"]; ok {
			var decisions []spec.Decision
			if err := json.Unmarshal(data, &decisions); err == nil {
				br.Decisions = append(br.Decisions, decisions...)
			}
		}

		if data, ok := raw["strategies"]; ok {
			var strategies []spec.Strategy
			if err := json.Unmarshal(data, &strategies); err == nil {
				br.Strategies = append(br.Strategies, strategies...)
			}
		}

		if data, ok := raw["approaches"]; ok {
			var approaches []spec.Approach
			if err := json.Unmarshal(data, &approaches); err == nil {
				br.Approaches = append(br.Approaches, approaches...)
			}
		}

		if data, ok := raw["entities"]; ok {
			var entities []spec.Entity
			if err := json.Unmarshal(data, &entities); err == nil {
				br.Entities = append(br.Entities, entities...)
			}
		}

		if data, ok := raw["gaps"]; ok {
			var gaps []Gap
			if err := json.Unmarshal(data, &gaps); err == nil {
				br.Gaps = append(br.Gaps, gaps...)
			}
		}
	}

	return br, nil
}
