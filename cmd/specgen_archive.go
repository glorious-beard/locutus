package cmd

import (
	"log/slog"
	"path"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// specTypeToDir maps the SpecChange.Type identifier to the plural
// directory name under .borg/spec/. Keeping this as an explicit
// table avoids the "strategy" → "strategys" pitfall and makes the
// supported set obvious.
var specTypeToDir = map[string]string{
	"feature":  "features",
	"decision": "decisions",
	"strategy": "strategies",
	"approach": "approaches",
}

// archiveAbandonedNodes moves files for IDs the council didn't
// produce on this run from the active spec graph
// (.borg/spec/<type>/) to an archive root
// (.borg/spec/.archived/<timestamp>/<type>/), preserving the
// per-type subdirectory structure. The active graph stays clean
// for downstream tools (explain, justify, render); the archived
// data stays available for forensics or operator-driven recovery.
//
// Best-effort per file: a missing .md file (legitimate for some
// node types) or a per-file write/remove failure logs a warning
// and continues rather than aborting the whole archive. Partial
// archive is better than none — the operator already has the diff
// telling them which IDs were targeted.
//
// Returns the list of IDs that were successfully archived (had at
// least one file moved). Empty slice when there's nothing to
// archive; archiveRoot is not created in that case.
func archiveAbandonedNodes(fsys specio.FS, abandoned []agent.SpecChange, archiveRoot string) []string {
	if len(abandoned) == 0 {
		return nil
	}
	var archivedIDs []string
	for _, c := range abandoned {
		plural, ok := specTypeToDir[c.Type]
		if !ok {
			slog.Warn("archive: unknown spec type", "type", c.Type, "id", c.ID)
			continue
		}
		srcDir := path.Join(".borg/spec", plural)
		dstDir := path.Join(archiveRoot, plural)

		movedAny := false
		for _, ext := range []string{".json", ".md"} {
			src := path.Join(srcDir, c.ID+ext)
			dst := path.Join(dstDir, c.ID+ext)
			data, err := fsys.ReadFile(src)
			if err != nil {
				// Missing file is OK — for example, an .md may not
				// exist for nodes mid-restructure or for types that
				// don't generate sidecars on a particular run.
				continue
			}
			if err := fsys.MkdirAll(dstDir, 0o755); err != nil {
				slog.Warn("archive: mkdir failed", "dir", dstDir, "error", err)
				continue
			}
			if err := fsys.WriteFile(dst, data, 0o644); err != nil {
				slog.Warn("archive: write failed", "dst", dst, "error", err)
				continue
			}
			if err := fsys.Remove(src); err != nil {
				slog.Warn("archive: remove failed; archive copy still exists",
					"src", src, "dst", dst, "error", err)
				// Counted as moved — the archive copy is safe even
				// if the source removal failed; the operator can
				// clean up manually.
			}
			movedAny = true
		}
		if movedAny {
			archivedIDs = append(archivedIDs, c.ID)
		}
	}
	return archivedIDs
}
