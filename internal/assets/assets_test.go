package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSource creates a temp directory containing the named files (each
// with contents equal to its name) and returns the directory path.
func setupSource(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range files {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, data, 0o644))
	}
	return dir
}

func TestImportCopiesInlineImages(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{
		"diagram.png": []byte("png-bytes"),
	})
	fsys := specio.NewMemFS()

	body := "Look at this:\n\n![A diagram](diagram.png)\n"
	out, res, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Contains(t, out, "![A diagram](../assets/feat-x/diagram.png)",
		"body should be rewritten to point at copied location")
	assert.Equal(t, []string{".borg/spec/assets/feat-x/diagram.png"}, res.Imported)
	assert.Empty(t, res.Missing)

	written, err := fsys.ReadFile(".borg/spec/assets/feat-x/diagram.png")
	require.NoError(t, err)
	assert.Equal(t, []byte("png-bytes"), written)
}

func TestImportCopiesHTMLImages(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{
		"shot.png": []byte("ok"),
	})
	fsys := specio.NewMemFS()

	body := `<img src="shot.png" alt="x" width="200">`
	out, res, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Contains(t, out, `src="../assets/feat-x/shot.png"`)
	assert.Contains(t, out, `width="200"`, "other attributes should survive")
	assert.Equal(t, 1, len(res.Imported))
}

func TestImportLeavesRemoteRefsAlone(t *testing.T) {
	fsys := specio.NewMemFS()

	body := `![remote](https://example.com/img.png)
![data](data:image/png;base64,xyz)
<img src="http://example.com/x.png">
`
	out, res, err := Import(fsys, body, "/nonexistent", ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Equal(t, body, out, "remote refs should be untouched")
	assert.Empty(t, res.Imported)
	assert.Empty(t, res.Missing)
}

func TestImportRecordsMissingFiles(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{})
	fsys := specio.NewMemFS()

	body := `![oops](does-not-exist.png)`
	out, res, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Equal(t, body, out, "missing refs should remain unchanged")
	assert.Empty(t, res.Imported)
	assert.Equal(t, []string{"does-not-exist.png"}, res.Missing)
}

func TestImportNoSourceDirReturnsBodyUnchanged(t *testing.T) {
	fsys := specio.NewMemFS()

	body := `![x](a.png)`
	out, res, err := Import(fsys, body, "", ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Equal(t, body, out)
	assert.Empty(t, res.Imported)
	assert.Empty(t, res.Missing)
}

func TestImportDeduplicatesRepeatedRefs(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{
		"d.png": []byte("ok"),
	})
	fsys := specio.NewMemFS()

	body := "![a](d.png)\n![b](d.png)\n"
	_, res, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Equal(t, 1, len(res.Imported), "the same file referenced twice should be copied once")
}

func TestImportHandlesFilenameCollisions(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{
		"a/shot.png": []byte("from-a"),
		"b/shot.png": []byte("from-b"),
	})
	fsys := specio.NewMemFS()

	body := "![a](a/shot.png)\n![b](b/shot.png)\n"
	out, res, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	require.Equal(t, 2, len(res.Imported))
	assert.Contains(t, res.Imported, ".borg/spec/assets/feat-x/shot.png")
	assert.Contains(t, res.Imported, ".borg/spec/assets/feat-x/shot-1.png",
		"second file with the same basename should get a numeric suffix")

	// The body should reference both copied locations.
	assert.True(t, strings.Contains(out, "shot.png") && strings.Contains(out, "shot-1.png"))
}

func TestImportPreservesAltText(t *testing.T) {
	sourceDir := setupSource(t, map[string][]byte{
		"d.png": []byte("ok"),
	})
	fsys := specio.NewMemFS()

	body := `![Architecture diagram showing the data flow](d.png)`
	out, _, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.Contains(t, out, "![Architecture diagram showing the data flow](")
}

func TestImportDropsImageTitleAttribute(t *testing.T) {
	// Markdown allows: ![alt](path "title"). We do not preserve the title;
	// document the behavior so it doesn't surprise readers.
	sourceDir := setupSource(t, map[string][]byte{
		"d.png": []byte("ok"),
	})
	fsys := specio.NewMemFS()

	body := `![a](d.png "tooltip text")`
	out, _, err := Import(fsys, body, sourceDir, ".borg/spec/assets/feat-x", ".borg/spec/features/feat-x.md")
	require.NoError(t, err)

	assert.NotContains(t, out, "tooltip text",
		"title attribute is intentionally dropped during import rewrite")
}
