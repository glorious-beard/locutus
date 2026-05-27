package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/render"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
)

// recentBiasedLimit caps the spec_biased events surfaced in the
// snapshot's "Recent biases" section. Picked empirically — small
// enough to keep the section scannable, large enough to surface a
// few hours of `--with` activity.
const recentBiasedLimit = 10

// biasPreviewLength bounds the inline preview of a bias text in
// the snapshot. The full bias is preserved in the history event;
// the preview is a UX-only truncation.
const biasPreviewLength = 80

// GatherSnapshotData loads the spec graph + workstream activity and
// produces the SnapshotData struct that backs both the Markdown and
// JSON formats. Filters constrain the visible set; pass an empty
// SnapshotFilters to include everything.
//
// No LLM calls; pure read of `.borg/` and `.locutus/workstreams/`.
// Project name comes from `.borg/manifest.json` when available.
// Recent --with cascade roots (DJ-138) come from `.borg/history`.
func GatherSnapshotData(fsys specio.FS, filters render.SnapshotFilters) (render.SnapshotData, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return render.SnapshotData{}, err
	}
	stages := spec.DeriveStages(loaded, fsys)

	projectName := projectNameFromManifest(fsys)
	data := render.BuildSnapshotData(loaded, stages, projectName, filters)
	data.RecentBiased = gatherRecentBiased(fsys)
	return data, nil
}

// gatherRecentBiased reads the history log for recent spec_biased
// events (DJ-138 phase 6) and converts them into the snapshot's
// rendered form. Errors are swallowed — a missing or unreadable
// history log degrades the snapshot rather than failing it.
func gatherRecentBiased(fsys specio.FS) []render.SnapshotBiasedEvent {
	hist := history.NewHistorian(fsys, ".borg/history")
	events, err := hist.RecentBiased(recentBiasedLimit)
	if err != nil || len(events) == 0 {
		return nil
	}
	out := make([]render.SnapshotBiasedEvent, 0, len(events))
	for _, ev := range events {
		entry := render.SnapshotBiasedEvent{
			EventID:   ev.ID,
			Timestamp: ev.Timestamp,
			TargetID:  ev.TargetID,
		}
		if ev.Biased != nil {
			entry.BiasPreview = truncatePreview(ev.Biased.BiasText, biasPreviewLength)
			entry.ACPSessionID = ev.Biased.ACPSessionID
			entry.BlastRadius = formatBlastRadius(ev.Biased.BlastRadius)
		} else {
			// Defensive: a spec_biased event without a Biased payload
			// is malformed (pre-DJ-138 history wouldn't have these at
			// all); fall back to rationale-as-preview.
			entry.BiasPreview = truncatePreview(ev.Rationale, biasPreviewLength)
		}
		out = append(out, entry)
	}
	return out
}

func truncatePreview(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// The ellipsis "…" is 3 bytes in UTF-8; reserve room so the
	// returned string stays within the max byte budget.
	const ellipsis = "…"
	cut := max - len(ellipsis)
	if cut <= 0 {
		return s[:max]
	}
	return s[:cut] + ellipsis
}

func formatBlastRadius(br history.BlastRadiusEstimate) string {
	return fmt.Sprintf("%d dec, %d feat, %d strat, %d app",
		br.DecisionsTouched, br.FeaturesRewritten, br.StrategiesRewritten, br.ApproachesDrifted)
}

// projectNameFromManifest reads `.borg/manifest.json` for the project
// name. Returns empty string when the manifest is missing or
// unreadable; the snapshot header degrades gracefully.
func projectNameFromManifest(fsys specio.FS) string {
	data, err := fsys.ReadFile(".borg/manifest.json")
	if err != nil {
		return ""
	}
	var m spec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m.ProjectName
}
