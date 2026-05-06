package cmd

import (
	"encoding/json"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// GatherSnapshotData loads the spec graph + workstream activity and
// produces the SnapshotData struct that backs both the Markdown and
// JSON formats. Filters constrain the visible set; pass an empty
// SnapshotFilters to include everything.
//
// No LLM calls; pure read of `.borg/` and `.locutus/workstreams/`.
// Project name comes from `.borg/manifest.json` when available.
func GatherSnapshotData(fsys specio.FS, filters render.SnapshotFilters) (render.SnapshotData, error) {
	loaded, err := spec.LoadSpec(fsys)
	if err != nil {
		return render.SnapshotData{}, err
	}
	stages := spec.DeriveStages(loaded, fsys)

	projectName := projectNameFromManifest(fsys)
	return render.BuildSnapshotData(loaded, stages, projectName, filters), nil
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
