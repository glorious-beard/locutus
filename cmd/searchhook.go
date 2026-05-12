package cmd

import (
	"log/slog"
	"path/filepath"

	"github.com/blugelabs/bluge"
	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/specio"
)

// registerSearchHook installs the production callback that mirrors
// every spec write into the on-disk Bluge index. When the process
// isn't running inside a Locutus project the registration is a no-op
// — leaving onSpecWrite nil keeps `locutus init` and similar
// bootstrap verbs unaffected.
//
// The callback is best-effort: errors on the index path are logged
// and swallowed so spec writes never fail because the index is
// unhappy. The fingerprint-mismatch path in search.Open catches up on
// the next process start.
func registerSearchHook() {
	root, err := specio.FindProjectRootFromCwd()
	if err != nil {
		return
	}
	fsys := specio.NewOSFS(root)
	indexPath := filepath.Join(root, search.IndexDir)
	specio.SetSpecWriteCallback(makeSearchHook(fsys, indexPath))
}

// makeSearchHook builds the callback used by registerSearchHook.
// Split out so the integration test can install the same logic
// against a fixture project without depending on cwd.
func makeSearchHook(fsys specio.FS, indexPath string) specio.SpecWriteCallback {
	return func(kind, id string, deleted bool) {
		w, err := search.OpenWriterWithRetry(indexPath)
		if err != nil {
			slog.Warn("search: hook skip — writer unavailable", "kind", kind, "id", id, "deleted", deleted, "err", err)
			return
		}
		defer func() { _ = w.Close() }()

		if deleted {
			if err := w.Delete(bluge.Identifier(id)); err != nil {
				slog.Warn("search: hook delete failed", "kind", kind, "id", id, "err", err)
			}
			return
		}

		doc, err := search.BuildDocument(fsys, kind, id)
		if err != nil {
			slog.Warn("search: hook build doc failed", "kind", kind, "id", id, "err", err)
			return
		}
		if err := w.Update(doc.ID(), doc); err != nil {
			slog.Warn("search: hook update failed", "kind", kind, "id", id, "err", err)
		}
	}
}
