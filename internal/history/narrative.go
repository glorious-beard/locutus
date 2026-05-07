package history

// Narrative generation — DJ-026 layer 2, landed 2026-04-23.
//
// The historian already ships layer 1 (structured JSON events). This file
// adds the LLM-authored layer 2 that DJ-026 described but never
// implemented: a manifest (`summary.md`) at the top of `.borg/history/`
// plus per-target detail files under `details/<target-id>.md`. The
// manifest is cheap (archivist agent, fast tier) and is regenerated any
// time the event set changes; detail files are expensive (analyst agent,
// balanced tier) and are regenerated only for targets whose own event
// subset has changed since the last pass. Both layers carry an embedded
// hash in their frontmatter so re-runs can skip the LLM entirely when
// nothing new has happened.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GenerateFn is the narrow LLM contract this package uses. It takes a
// user-prompt payload and returns the generated markdown. The system
// prompt (agent identity, role, behavioral rules) is the caller's
// responsibility — typically loaded from an agent definition file per
// DJ-036 and baked into the closure — so this package stays free of
// hard-coded agent personas and free of an `agent`-package import
// (which would cycle: agent already depends on history for events).
type GenerateFn func(ctx context.Context, userPrompt string) (string, error)

// NarrativeConfig bundles the options `GenerateNarrative` accepts. LLM
// access is expressed as `GenerateFn` callbacks so the history package
// stays free of a direct agent-package dependency (agent already imports
// history). The cmd/history path wraps an `agent.AgentExecutor` into both fns.
type NarrativeConfig struct {
	// Generate is the default LLM callback if ArchivistFn / AnalystFn
	// aren't set. Tests can pass a single MockLLM-backed adapter through
	// all three slots.
	Generate GenerateFn

	// ArchivistFn is the fast-tier callback that writes the manifest.
	// Falls back to Generate when nil.
	ArchivistFn GenerateFn

	// AnalystFn is the balanced-tier callback that writes each detail
	// file. Falls back to Generate when nil.
	AnalystFn GenerateFn

	// Force bypasses the change-hash debounce and regenerates
	// unconditionally.
	Force bool

	// Since and Until scope the event window. Nil means unbounded.
	Since *time.Time
	Until *time.Time

	// MinEventsForDetail is the threshold a target must meet before it
	// gets its own detail file. Zero defaults to 2 — one event is
	// usually too thin to warrant narrative expansion.
	MinEventsForDetail int

	// OutputDir overrides where the narrative summary + details/ are
	// written. Empty means "co-located with the event store" (the
	// historian's dir) — preserved for tests and back-compat. Production
	// callers route narrative output to .locutus/history/ since the
	// rendered prose is a regenerable cache, not source-of-truth (DJ-103).
	OutputDir string
}

// NarrativeReport summarises what `GenerateNarrative` did.
type NarrativeReport struct {
	Skipped            bool     // true when the debounce hash matched and nothing ran
	ArchivistCalls     int      // times the archivist LLM was invoked (0 or 1 per run)
	AnalystCalls       int      // times the analyst LLM was invoked (one per regenerated detail)
	DetailsRegenerated []string // target IDs whose detail file was rewritten this run
	DetailsSkipped     []string // target IDs whose detail file was left unchanged
}

// hashMarker is the magic comment the manifest and detail files carry in
// their YAML-ish frontmatter so `GenerateNarrative` can read back the
// hash it stored last time. Stored as an HTML comment to survive any
// markdown renderer and stay out of visible prose.
const hashMarker = "<!-- locutus-narrative-hash:"

var hashMarkerRE = regexp.MustCompile(`(?m)^<!--\s*locutus-narrative-hash:\s*([A-Za-z0-9:+\-]+)\s*-->`)

// defaultMinEventsForDetail is the threshold for detail-file eligibility
// when NarrativeConfig.MinEventsForDetail isn't set by the caller.
const defaultMinEventsForDetail = 2

// GenerateNarrative runs the DJ-026 layer-2 pipeline: loads events,
// applies the time-window filter, computes a deterministic hash of the
// filtered set, and decides per component (manifest, each detail) whether
// to skip or regenerate. The archivist writes the manifest; the analyst
// writes detail files for qualifying targets whose own event subset has
// changed.
//
// On success the manifest at <dir>/summary.md and detail files under
// <dir>/details/*.md reflect the current event state. On debounce-skip
// the existing files are left untouched and `NarrativeReport.Skipped` is
// true.
func (h *Historian) GenerateNarrative(ctx context.Context, cfg NarrativeConfig) (*NarrativeReport, error) {
	archivist := cfg.ArchivistFn
	if archivist == nil {
		archivist = cfg.Generate
	}
	analyst := cfg.AnalystFn
	if analyst == nil {
		analyst = cfg.Generate
	}
	if archivist == nil || analyst == nil {
		return nil, fmt.Errorf("narrative: archivist and analyst callbacks are required (set NarrativeConfig.Generate or both ArchivistFn+AnalystFn)")
	}
	minForDetail := cfg.MinEventsForDetail
	if minForDetail <= 0 {
		minForDetail = defaultMinEventsForDetail
	}

	events, err := h.Events()
	if err != nil {
		return nil, fmt.Errorf("narrative: load events: %w", err)
	}
	events = filterEventsByWindow(events, cfg.Since, cfg.Until)

	report := &NarrativeReport{}

	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = h.dir
	}
	manifestPath := path.Join(outputDir, "summary.md")
	overallHash := hashEvents(events)

	storedManifestHash := readEmbeddedHash(h.fsys, manifestPath)
	manifestDue := cfg.Force || storedManifestHash != overallHash

	// Per-target hashes for detail-file debounce.
	eventsByTarget := groupByTarget(events)
	targetHashes := make(map[string]string, len(eventsByTarget))
	for tid, evts := range eventsByTarget {
		targetHashes[tid] = hashEvents(evts)
	}

	eligibleTargets := make([]string, 0)
	for tid, evts := range eventsByTarget {
		if len(evts) < minForDetail {
			continue
		}
		eligibleTargets = append(eligibleTargets, tid)
	}
	sort.Strings(eligibleTargets)

	// Decide which detail files need regen. Force bypasses the per-target
	// debounce too; otherwise each eligible target checks its own stored
	// hash against the current subset hash.
	detailsToRegen := make([]string, 0)
	for _, tid := range eligibleTargets {
		detailPath := path.Join(outputDir, "details", tid+".md")
		if cfg.Force {
			detailsToRegen = append(detailsToRegen, tid)
			continue
		}
		stored := readEmbeddedHash(h.fsys, detailPath)
		if stored != targetHashes[tid] {
			detailsToRegen = append(detailsToRegen, tid)
		} else {
			report.DetailsSkipped = append(report.DetailsSkipped, tid)
		}
	}

	if !manifestDue && len(detailsToRegen) == 0 {
		report.Skipped = true
		return report, nil
	}

	// Manifest regen.
	if manifestDue {
		body, err := invokeArchivist(ctx, archivist, events, cfg)
		if err != nil {
			return report, fmt.Errorf("narrative: archivist: %w", err)
		}
		body = stampHash(body, overallHash)
		if err := h.fsys.MkdirAll(outputDir, 0o755); err != nil {
			return report, fmt.Errorf("narrative: mkdir %s: %w", outputDir, err)
		}
		if err := h.fsys.WriteFile(manifestPath, []byte(body), 0o644); err != nil {
			return report, fmt.Errorf("narrative: write manifest: %w", err)
		}
		report.ArchivistCalls = 1
	}

	// Detail regens.
	for _, tid := range detailsToRegen {
		body, err := invokeAnalyst(ctx, analyst, tid, eventsByTarget[tid], cfg)
		if err != nil {
			return report, fmt.Errorf("narrative: analyst %s: %w", tid, err)
		}
		body = stampHash(body, targetHashes[tid])
		detailPath := path.Join(outputDir, "details", tid+".md")
		if err := h.fsys.MkdirAll(path.Dir(detailPath), 0o755); err != nil {
			return report, fmt.Errorf("narrative: mkdir %s: %w", path.Dir(detailPath), err)
		}
		if err := h.fsys.WriteFile(detailPath, []byte(body), 0o644); err != nil {
			return report, fmt.Errorf("narrative: write detail %s: %w", tid, err)
		}
		report.AnalystCalls++
		report.DetailsRegenerated = append(report.DetailsRegenerated, tid)
	}

	return report, nil
}

// filterEventsByWindow drops events outside the [since, until] range.
// Nil bounds are unbounded on that side.
func filterEventsByWindow(events []Event, since, until *time.Time) []Event {
	if since == nil && until == nil {
		return events
	}
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if since != nil && e.Timestamp.Before(*since) {
			continue
		}
		if until != nil && e.Timestamp.After(*until) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// groupByTarget partitions events by TargetID. Events with an empty
// TargetID are dropped — a narrative about an unnamed target has no
// entry point.
func groupByTarget(events []Event) map[string][]Event {
	out := make(map[string][]Event)
	for _, e := range events {
		if e.TargetID == "" {
			continue
		}
		out[e.TargetID] = append(out[e.TargetID], e)
	}
	return out
}

// EventsHash returns the deterministic hash of the events stored in
// the historian's directory, applying the same time-window filter
// GenerateNarrative would. Used by cmd/history to detect a stale
// narrative cache without re-running the LLM. since/until may be
// nil for unbounded windows.
func (h *Historian) EventsHash(since, until *time.Time) (string, error) {
	events, err := h.Events()
	if err != nil {
		return "", err
	}
	events = filterEventsByWindow(events, since, until)
	return hashEvents(events), nil
}

// CachedNarrativeHash reads the embedded hash from a previously
// rendered summary.md. Returns "" when the file doesn't exist or
// the marker is absent — both treated as "no cache; regenerate."
func (h *Historian) CachedNarrativeHash(outputDir string) string {
	if outputDir == "" {
		outputDir = h.dir
	}
	return readEmbeddedHash(h.fsys, path.Join(outputDir, "summary.md"))
}

// hashEvents produces a stable SHA-256 over the events' identifying
// fields (ID, Timestamp, Kind, TargetID, OldValue, NewValue, Rationale).
// Used for debounce: two invocations over the same event set produce
// the same hash and skip the LLM. Omits Alternatives because those
// frequently carry LLM-generated prose whose whitespace can differ
// across runs without a meaningful change.
func hashEvents(events []Event) string {
	type canonical struct {
		ID        string    `json:"id"`
		Timestamp time.Time `json:"ts"`
		Kind      string    `json:"kind"`
		TargetID  string    `json:"target_id"`
		OldValue  string    `json:"old,omitempty"`
		NewValue  string    `json:"new,omitempty"`
		Rationale string    `json:"rationale,omitempty"`
	}
	sorted := make([]canonical, len(events))
	for i, e := range events {
		sorted[i] = canonical{
			ID: e.ID, Timestamp: e.Timestamp, Kind: e.Kind, TargetID: e.TargetID,
			OldValue: e.OldValue, NewValue: e.NewValue, Rationale: e.Rationale,
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	data, _ := json.Marshal(sorted)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readEmbeddedHash extracts the `<!-- locutus-narrative-hash: ... -->`
// marker from a narrative file. Returns "" if the file doesn't exist or
// the marker isn't present — both are treated as "not yet generated."
func readEmbeddedHash(fsys interface {
	ReadFile(string) ([]byte, error)
}, filePath string) string {
	data, err := fsys.ReadFile(filePath)
	if err != nil {
		return ""
	}
	match := hashMarkerRE.FindSubmatch(data)
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}

// stampHash ensures the given body contains a hash marker. If the body
// already carries a marker (the LLM included one from the prompt), it's
// rewritten to the authoritative value. Otherwise the marker is inserted
// after the first line of the body.
func stampHash(body, hash string) string {
	marker := fmt.Sprintf("<!-- locutus-narrative-hash: %s -->", hash)
	if hashMarkerRE.MatchString(body) {
		return hashMarkerRE.ReplaceAllString(body, marker)
	}
	// Insert after the first line (typically the markdown H1). If the
	// body is empty or the first line is missing, prepend.
	idx := strings.Index(body, "\n")
	if idx == -1 {
		return body + "\n\n" + marker + "\n"
	}
	return body[:idx+1] + "\n" + marker + "\n" + body[idx+1:]
}

// invokeArchivist asks the archivist callback to render the summary.
// Input: every event in the filtered window in chronological order,
// with full structured payload (old/new values, rationale,
// alternatives). The archivist's job is to turn that record into
// narrative prose; data-only projection per DJ-097.
//
// Per-rationale: the archivist used to receive only id/timestamp/
// kind/target/rationale, paired with a "one-line summary per event
// is plenty" directive. Result: a summary worse than `git log`.
// DJ-103 unstarves it — full payload plus the substantive prompt
// in the agent's .md file.
func invokeArchivist(ctx context.Context, fn GenerateFn, events []Event, cfg NarrativeConfig) (string, error) {
	var b strings.Builder
	b.WriteString("Write the project-history summary per the archivist agent's instructions. The events below are the full record for the requested window.\n\n")
	if cfg.Since != nil || cfg.Until != nil {
		b.WriteString("## Event window\n")
		if cfg.Since != nil {
			fmt.Fprintf(&b, "Since: %s\n", cfg.Since.Format("2006-01-02"))
		}
		if cfg.Until != nil {
			fmt.Fprintf(&b, "Until: %s\n", cfg.Until.Format("2006-01-02"))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Events\n")
	for _, e := range events {
		fmt.Fprintf(&b, "### %s\n", e.ID)
		fmt.Fprintf(&b, "- timestamp: %s\n", e.Timestamp.Format(time.RFC3339))
		fmt.Fprintf(&b, "- kind: %s\n", e.Kind)
		fmt.Fprintf(&b, "- target: %s\n", e.TargetID)
		if e.Rationale != "" {
			fmt.Fprintf(&b, "- rationale: %s\n", quoteForPrompt(e.Rationale))
		}
		if e.OldValue != "" {
			fmt.Fprintf(&b, "- old_value: %s\n", quoteForPrompt(e.OldValue))
		}
		if e.NewValue != "" {
			fmt.Fprintf(&b, "- new_value: %s\n", quoteForPrompt(e.NewValue))
		}
		if len(e.Alternatives) > 0 {
			fmt.Fprintf(&b, "- alternatives:\n")
			for _, a := range e.Alternatives {
				fmt.Fprintf(&b, "  - %s\n", quoteForPrompt(a))
			}
		}
		b.WriteString("\n")
	}
	return fn(ctx, b.String())
}

// quoteForPrompt formats a possibly-multiline string so the
// archivist can read it without confusion. Replaces newlines with
// `\n` literals when short; uses fenced block when long. The
// archivist's prompt expects the events as "data", and ambiguous
// indentation in long old_value/new_value blocks would corrupt the
// markdown structure.
func quoteForPrompt(s string) string {
	if !strings.ContainsAny(s, "\n\r") && len(s) < 200 {
		return strings.TrimSpace(s)
	}
	return "<<\n" + strings.TrimRight(s, "\n") + "\n>>"
}

// invokeAnalyst asks the balanced-tier callback to render a per-target
// detail narrative. Input: the event subset for this target plus the
// target ID. The analyst is encouraged to infer causation and motivation
// (within the bounds of what the events support) — that's where the
// manifest stops and the detail file earns its keep.
func invokeAnalyst(ctx context.Context, fn GenerateFn, targetID string, events []Event, cfg NarrativeConfig) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Write a narrative detail file for the spec node `%s`. Explain the arc of its history — what prompted each change, what alternatives were considered (when the events mention any), what motivations or constraints shaped the direction. Do not fabricate reasons the events don't support; when an event's rationale is sparse, say so.\n\n", targetID)
	b.WriteString("Format: a single markdown document starting with `# " + targetID + "`. No timeline table — the manifest already has that. Focus on the story.\n\n")
	b.WriteString("## Events for this target\n")
	for _, e := range events {
		fmt.Fprintf(&b, "- [%s] %s | %s | old=%q new=%q rationale=%q alternatives=%v\n",
			e.ID, e.Timestamp.Format(time.RFC3339), e.Kind, e.OldValue, e.NewValue, e.Rationale, e.Alternatives)
	}
	return fn(ctx, b.String())
}

