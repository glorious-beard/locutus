package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/assets"
	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// ImportCmd admits a feature or bug from a markdown document. Admission is
// gated by an LLM intake call that derives a stable id/title from the
// document and (when GOALS.md is present) evaluates whether to accept it.
// --skip-triage bypasses the LLM call entirely; --dry-run reports the
// derived id and intended destination without writing.
type ImportCmd struct {
	Path       string `arg:"" help:"Path to markdown document." type:"existingfile"`
	Type       string `help:"Admit as 'feature' (default) or 'bug'." default:"feature" enum:"feature,bug"`
	SkipTriage bool   `help:"Skip the LLM intake call; derive metadata deterministically (frontmatter > filename)."`
	NoPlan     bool   `help:"Admit only — do not run the planning pass that decomposes the doc into decisions/strategies/approaches."`
	DryRun     bool   `help:"Preview intake and intended destination; do not write."`
}

// ImportResult summarises an import attempt.
type ImportResult struct {
	Accepted       bool                `json:"accepted"`
	Verdict        *agent.IntakeResult `json:"verdict,omitempty"`
	Destination    string              `json:"destination,omitempty"`
	FeatureID      string              `json:"feature_id,omitempty"`
	BugID          string              `json:"bug_id,omitempty"`
	ImportedAssets []string            `json:"imported_assets,omitempty"`
	MissingAssets  []string            `json:"missing_assets,omitempty"`
	Generated      *GenerationSummary  `json:"generated,omitempty"`
	DryRun         bool                `json:"dry_run"`
	SkippedTriage  bool                `json:"skipped_triage"`
	SkippedPlan    bool                `json:"skipped_plan,omitempty"`
}

func (c *ImportCmd) Run(ctx context.Context, cli *CLI) error {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	fsys, root, err := projectFS()
	if err != nil {
		return err
	}

	// Construct the LLM up front when we know we'll need it (intake
	// runs unless --skip-triage). Wrapping in a SessionRecorder means
	// every council call lands under .locutus/sessions/.
	var llm agent.LLM
	var rec *agent.SessionRecorder
	if !c.SkipTriage {
		llm, rec, err = recordingLLM(fsys, root, "import "+c.Path)
		if err != nil {
			return err
		}
	}

	result, err := RunImport(ctx, llm, fsys, data, c.Path, c.Type, c.SkipTriage, c.NoPlan, c.DryRun)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printImportResult(result)
	if rec != nil {
		fmt.Printf("Session: %s\n", rec.Path())
	}
	if !result.Accepted {
		return ExitCode(1)
	}
	return nil
}

// RunImport is the shared implementation used by both the CLI and MCP
// handlers. It runs the LLM intake call (deriving id/title and, when
// GOALS.md is present, an admission verdict), then — if accepted and not
// dry-running — writes the spec node.
//
// llm is the LLM to use for intake and the planning pass. The CLI passes
// a recording-wrapped LLM so council calls land under
// .locutus/sessions/. May be nil only when skipTriage=true AND noPlan=true
// (no LLM-using paths fire).
//
// sourcePath is optional: when present, it's used as a deterministic
// fallback for id/title when --skip-triage is set or the LLM is
// unavailable. noPlan suppresses the post-admission planning pass.
func RunImport(ctx context.Context, llm agent.LLM, fsys specio.FS, data []byte, sourcePath, kind string, skipTriage, noPlan, dryRun bool) (*ImportResult, error) {
	result := &ImportResult{DryRun: dryRun, SkippedTriage: skipTriage, SkippedPlan: noPlan}

	// 1. Resolve metadata: frontmatter > LLM intake > filename fallback.
	meta, intake, err := resolveImportMetadata(ctx, llm, fsys, data, sourcePath, kind, skipTriage)
	if err != nil {
		return nil, err
	}
	if intake != nil {
		result.Verdict = intake
		// Triage gate fires only when GOALS.md was present (i.e. the LLM
		// was asked to evaluate). In the no-GOALS case the LLM still
		// returns Accepted=true by instruction, so this check is a no-op
		// there.
		if intake.Reason != "" && (!intake.Accepted || intake.Duplicate) {
			result.Accepted = false
			return result, nil
		}
	}
	result.Accepted = true

	// 2. Build destination + persist (or preview).
	switch kind {
	case "bug":
		// Bugs deliberately do NOT trigger the planning pass. Per
		// DJ-068, an admitted bug is a per-incident artifact whose
		// decisions/strategies/approaches are inherited from its
		// parent Feature — generating a fresh graph for each bug
		// would duplicate spec content and clutter the graph with
		// per-incident nodes. If a bug surfaces a previously-undecided
		// architectural question, the user runs `refine <feature-id>`
		// (or edits decisions directly) to fold the new constraint in.
		result.BugID = meta.id
		result.Destination = ".borg/spec/bugs/" + meta.id
		if dryRun {
			return result, nil
		}
		assetRes, err := importAssetsForNode(fsys, meta, sourcePath, "bugs")
		if err != nil {
			return nil, err
		}
		result.ImportedAssets = assetRes.Imported
		result.MissingAssets = assetRes.Missing
		if _, err := writeBug(fsys, meta); err != nil {
			return nil, err
		}
	default:
		result.FeatureID = meta.id
		result.Destination = ".borg/spec/features/" + meta.id
		if dryRun {
			return result, nil
		}
		assetRes, err := importAssetsForNode(fsys, meta, sourcePath, "features")
		if err != nil {
			return nil, err
		}
		result.ImportedAssets = assetRes.Imported
		result.MissingAssets = assetRes.Missing
		if _, err := writeFeature(fsys, meta); err != nil {
			return nil, err
		}

		// 3. Planning pass: decompose the admitted feature into decisions,
		// strategies, and approaches. Skipped on --skip-triage (no LLM
		// available) and --no-plan (caller wants admission-only).
		if !skipTriage && !noPlan {
			gen, err := runFeatureGeneration(ctx, llm, fsys, meta)
			if err != nil {
				return result, fmt.Errorf("planning pass: %w", err)
			}
			result.Generated = gen
		}
	}
	return result, nil
}

// runFeatureGeneration runs the spec-generation LLM call against the
// admitted feature, treating the feature body as the document and
// extending the existing spec graph with the LLM's output.
func runFeatureGeneration(ctx context.Context, llm agent.LLM, fsys specio.FS, meta *importMetadata) (*GenerationSummary, error) {
	if llm == nil {
		return nil, fmt.Errorf("planning pass: no LLM provided")
	}
	goalsBody, _ := readGoals(fsys)
	if strings.TrimSpace(goalsBody) == "" {
		// Without GOALS the LLM has no project context to plan against;
		// admission already happened, skip planning rather than ask the
		// LLM to invent goals.
		return nil, nil
	}
	existing := loadExistingSpec(fsys)
	return runSpecGeneration(ctx, llm, fsys, agent.SpecGenRequest{
		GoalsBody:    goalsBody,
		DocumentBody: meta.body,
		DocumentID:   meta.id,
		Existing:     existing,
	})
}

// importAssetsForNode walks meta.body for image references, copies any
// local files into .borg/spec/assets/<id>/ on fsys, and rewrites meta.body
// in place so persisted JSON and md both reference the copied location.
//
// nodeDir is "features" or "bugs" — used to compute the relative ref from
// the .md back to the assets directory.
func importAssetsForNode(fsys specio.FS, meta *importMetadata, sourcePath, nodeDir string) (*assets.Result, error) {
	var sourceDir string
	if sourcePath != "" {
		abs, err := filepath.Abs(sourcePath)
		if err != nil {
			return &assets.Result{}, fmt.Errorf("resolve source path: %w", err)
		}
		sourceDir = filepath.Dir(abs)
	}

	destDir := filepath.Join(".borg/spec/assets", meta.id)
	mdPath := filepath.Join(".borg/spec", nodeDir, meta.id+".md")

	rewritten, res, err := assets.Import(fsys, meta.body, sourceDir, destDir, mdPath)
	if err != nil {
		return res, err
	}
	meta.body = rewritten
	return res, nil
}

func printImportResult(r *ImportResult) {
	switch {
	case r.Verdict != nil && r.Verdict.Reason != "":
		status := "rejected"
		switch {
		case r.Verdict.Duplicate:
			status = fmt.Sprintf("duplicate of %s", r.Verdict.DuplicateOf)
		case r.Verdict.Accepted:
			status = "accepted"
		}
		fmt.Printf("Intake verdict: %s\n", status)
		fmt.Printf("Reason: %s\n", r.Verdict.Reason)
	case r.SkippedTriage:
		fmt.Println("Intake: skipped (--skip-triage).")
	default:
		fmt.Println("Intake: GOALS.md not found; admitted without evaluation.")
	}
	if !r.Accepted {
		return
	}
	if r.DryRun {
		fmt.Printf("Dry-run: would create %s at %s.\n", nonempty(r.FeatureID, r.BugID), r.Destination)
		return
	}
	fmt.Printf("Imported %s at %s.\n", nonempty(r.FeatureID, r.BugID), r.Destination)
	if len(r.ImportedAssets) > 0 {
		fmt.Printf("Imported %d asset(s):\n", len(r.ImportedAssets))
		for _, a := range r.ImportedAssets {
			fmt.Printf("  - %s\n", a)
		}
	}
	if len(r.MissingAssets) > 0 {
		fmt.Printf("Warning: %d referenced asset(s) not found on disk:\n", len(r.MissingAssets))
		for _, a := range r.MissingAssets {
			fmt.Printf("  - %s\n", a)
		}
	}
	if r.Generated != nil {
		fmt.Printf("Planning: %d feature update(s), %d decision(s), %d strategy(ies), %d approach(es).\n",
			r.Generated.Features, r.Generated.Decisions, r.Generated.Strategies, r.Generated.Approaches)
		if len(r.Generated.IntegrityWarnings) > 0 {
			fmt.Printf("  %d dangling reference(s) stripped from the LLM output:\n", len(r.Generated.IntegrityWarnings))
			for _, w := range r.Generated.IntegrityWarnings {
				fmt.Printf("    - %s\n", w)
			}
		}
	} else if r.SkippedPlan {
		fmt.Println("Planning: skipped (--no-plan).")
	}
}

func nonempty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readGoals returns the body of GOALS.md and whether one was found. Looks at
// .borg/GOALS.md (scaffolded location) and GOALS.md (root).
func readGoals(fsys specio.FS) (string, bool) {
	if data, err := fsys.ReadFile(".borg/GOALS.md"); err == nil {
		return string(data), true
	}
	if data, err := fsys.ReadFile("GOALS.md"); err == nil {
		return string(data), true
	}
	return "", false
}

// importMetadata holds the resolved id/title/body plus any kind-specific
// fields (bug severity, parent feature) carried from optional input
// frontmatter.
type importMetadata struct {
	kind        string
	id          string
	title       string
	body        string
	bugSeverity string
	bugFeature  string
}

// importFrontmatter is the optional YAML override schema. All fields are
// optional; missing fields are filled in by the LLM intake call or by
// deterministic fallback.
type importFrontmatter struct {
	ID        string `yaml:"id"`
	Title     string `yaml:"title"`
	Type      string `yaml:"type"`
	Severity  string `yaml:"severity"`
	FeatureID string `yaml:"feature_id"`
}

// resolveImportMetadata builds the id/title used to admit the document. The
// resolution order is:
//
//  1. Optional YAML frontmatter (any subset; explicit overrides).
//  2. LLM intake call (the canonical path; also yields the triage verdict
//     when GOALS.md is present).
//  3. Deterministic fallback from sourcePath (slugified filename, first
//     heading or humanized basename).
//
// The intake call is skipped when skipTriage is true or no LLM provider is
// configured; in those cases steps 1 and 3 must cover all required fields,
// or the function errors.
//
// On success, intake is non-nil only when the LLM was actually called.
//
// llm is required when skipTriage=false; the caller is expected to pass
// the recording-wrapped LLM constructed at the top of the subcommand.
// Pass nil when skipTriage=true.
func resolveImportMetadata(ctx context.Context, llm agent.LLM, fsys specio.FS, data []byte, sourcePath, kind string, skipTriage bool) (*importMetadata, *agent.IntakeResult, error) {
	var fm importFrontmatter
	body, err := frontmatter.Parse(data, &fm)
	if err != nil {
		return nil, nil, err
	}

	meta := &importMetadata{
		kind:        kind,
		id:          fm.ID,
		title:       fm.Title,
		body:        body,
		bugSeverity: fm.Severity,
		bugFeature:  fm.FeatureID,
	}

	// LLM intake — the canonical path. Skipped only when the caller asked
	// to bypass it, in which case we fall back to deterministic
	// derivation. A missing LLM provider with skipTriage=false is a loud
	// error so the caller can surface "set GEMINI_API_KEY".
	var intake *agent.IntakeResult
	if !skipTriage {
		if llm == nil {
			return nil, nil, fmt.Errorf("intake required (skipTriage=false) but no LLM provided")
		}
		goalsBody, _ := readGoals(fsys)
		intake, err = agent.IntakeDocument(agent.WithRole(ctx, "intake"), llm, kind, string(data), goalsBody)
		if err != nil {
			return nil, nil, err
		}
		if meta.id == "" {
			meta.id = intake.ID
		}
		if meta.title == "" {
			meta.title = intake.Title
		}
	}

	// Deterministic fallbacks for anything still missing.
	if meta.id == "" {
		if sourcePath == "" {
			return nil, intake, fmt.Errorf("missing id: provide it via frontmatter, supply a source path, or remove --skip-triage so the LLM can derive one")
		}
		meta.id = derivedID(sourcePath, idPrefixForKind(kind))
	}
	if meta.title == "" {
		meta.title = firstHeading(body)
	}
	if meta.title == "" && sourcePath != "" {
		meta.title = humanizeBaseName(sourcePath)
	}

	return meta, intake, nil
}

func idPrefixForKind(kind string) string {
	if kind == "bug" {
		return "bug-"
	}
	return "feat-"
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// derivedID returns a slug derived from the file's base name, prefixed with
// prefix unless the slug already begins with it.
func derivedID(sourcePath, prefix string) string {
	base := filepath.Base(sourcePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	slug := nonSlugChars.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "untitled"
	}
	if strings.HasPrefix(slug, prefix) {
		return slug
	}
	return prefix + slug
}

// firstHeading returns the text of the first markdown ATX heading
// (`# Title`, `## Title`, …) in the body's first ~20 lines.
func firstHeading(body string) string {
	scanner := bufio.NewScanner(strings.NewReader(body))
	lines := 0
	for scanner.Scan() && lines < 20 {
		lines++
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

// humanizeBaseName turns a filename like "user-onboarding.md" into a title
// "User Onboarding". Used as a last-resort title fallback.
func humanizeBaseName(sourcePath string) string {
	base := filepath.Base(sourcePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || unicode.IsSpace(r)
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func writeFeature(fsys specio.FS, m *importMetadata) (*spec.Feature, error) {
	now := time.Now()
	feat := spec.Feature{
		ID:          m.id,
		Title:       m.title,
		Status:      spec.FeatureStatusProposed,
		Description: m.body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := specio.SavePair(fsys, ".borg/spec/features/"+m.id, feat, m.body); err != nil {
		return nil, err
	}
	return &feat, nil
}

func writeBug(fsys specio.FS, m *importMetadata) (*spec.Bug, error) {
	now := time.Now()
	bug := spec.Bug{
		ID:          m.id,
		Title:       m.title,
		FeatureID:   m.bugFeature,
		Severity:    spec.BugSeverity(m.bugSeverity),
		Status:      spec.BugStatusReported,
		Description: m.body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := specio.SavePair(fsys, ".borg/spec/bugs/"+m.id, bug, m.body); err != nil {
		return nil, err
	}
	return &bug, nil
}

// ImportFeature exists for tests that exercise the persistence layer
// directly without involving the LLM. Production callers should go through
// RunImport.
func ImportFeature(fsys specio.FS, input []byte, sourcePath string) (*spec.Feature, error) {
	meta, _, err := resolveImportMetadata(context.Background(), nil, fsys, input, sourcePath, "feature", true)
	if err != nil {
		return nil, err
	}
	return writeFeature(fsys, meta)
}

// ImportBug is the bug analogue of ImportFeature.
func ImportBug(fsys specio.FS, input []byte, sourcePath string) (*spec.Bug, error) {
	meta, _, err := resolveImportMetadata(context.Background(), nil, fsys, input, sourcePath, "bug", true)
	if err != nil {
		return nil, err
	}
	return writeBug(fsys, meta)
}
