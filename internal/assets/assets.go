// Package assets handles import of locally referenced images and other
// binary attachments from a markdown document. The package walks inline
// `![alt](path)` and `<img src="path">` references, copies any local file
// targets into a destination directory under the project's spec FS, and
// returns a rewritten body whose references point at the copied location.
//
// Remote URLs (http://, https://, data:) are left untouched. Missing files
// are reported back to the caller; the original reference in the body is
// left intact so the user can see what was missing.
package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chetan/locutus/internal/specio"
)

// inlineImagePattern matches GitHub-flavored markdown inline images:
//
//	![alt text](path/to/image.png)
//	![alt](image.png "optional title")
//
// Capture groups: 1=alt text, 2=path.
var inlineImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// htmlImagePattern matches HTML <img> tags. Capture group 1 is the src.
var htmlImagePattern = regexp.MustCompile(`(?i)<img\s+[^>]*src=["']([^"']+)["'][^>]*>`)

// Result reports the outcome of an asset import.
type Result struct {
	// Imported lists the FS-relative destination paths of files copied
	// into destDir. Useful for spec attachment and CLI reporting.
	Imported []string
	// Missing lists the original references that pointed at files not
	// found on disk. The corresponding refs in the body are unchanged.
	Missing []string
}

// Import scans body for image references, copies any local files into
// destDir on fsys, and returns the rewritten body alongside an itemized
// Result.
//
//   - sourceDir is the directory paths in body resolve relative to —
//     typically filepath.Dir(absoluteSourcePath). When empty (e.g. MCP
//     callers passing raw content with no source path), Import returns
//     the body unchanged.
//   - destDir is the FS-relative directory under which copied assets are
//     written (e.g. ".borg/spec/assets/feat-dashboard").
//   - mdPath is the FS-relative path the rewritten body will be stored at;
//     used to compute relative refs that point from that location to the
//     copied assets.
func Import(fsys specio.FS, body, sourceDir, destDir, mdPath string) (string, *Result, error) {
	res := &Result{}
	if sourceDir == "" {
		return body, res, nil
	}

	// Cache: absolute source path → FS-relative destination path. Lets a
	// document referencing the same image multiple times copy it once.
	seen := map[string]string{}
	mdDir := filepath.Dir(mdPath)

	rewrite := func(originalRef string) string {
		if isRemoteRef(originalRef) {
			return originalRef
		}

		srcPath := originalRef
		if !filepath.IsAbs(srcPath) {
			srcPath = filepath.Join(sourceDir, srcPath)
		}
		absSrc, err := filepath.Abs(srcPath)
		if err != nil {
			res.Missing = append(res.Missing, originalRef)
			return originalRef
		}

		if dest, ok := seen[absSrc]; ok {
			return relRef(mdDir, dest)
		}

		data, err := os.ReadFile(absSrc)
		if err != nil {
			res.Missing = append(res.Missing, originalRef)
			return originalRef
		}

		filename := uniqueFilename(fsys, destDir, filepath.Base(absSrc))
		dest := filepath.Join(destDir, filename)

		if err := fsys.MkdirAll(destDir, 0o755); err != nil {
			res.Missing = append(res.Missing, originalRef)
			return originalRef
		}
		if err := fsys.WriteFile(dest, data, 0o644); err != nil {
			res.Missing = append(res.Missing, originalRef)
			return originalRef
		}

		seen[absSrc] = dest
		res.Imported = append(res.Imported, dest)
		return relRef(mdDir, dest)
	}

	// Inline markdown images. Replace whole match so we can preserve the
	// alt text exactly and (deliberately) drop any optional title.
	body = inlineImagePattern.ReplaceAllStringFunc(body, func(match string) string {
		m := inlineImagePattern.FindStringSubmatch(match)
		alt, ref := m[1], m[2]
		return fmt.Sprintf("![%s](%s)", alt, rewrite(ref))
	})

	// HTML <img> tags. Replace just the src attribute so any other
	// attributes (width, alt, class…) survive.
	body = htmlImagePattern.ReplaceAllStringFunc(body, func(match string) string {
		m := htmlImagePattern.FindStringSubmatch(match)
		ref := m[1]
		newRef := rewrite(ref)
		if newRef == ref {
			return match
		}
		// Replace only the first occurrence of the original ref inside
		// the matched tag — guards against tags whose alt text happens
		// to repeat the src verbatim.
		return strings.Replace(match, ref, newRef, 1)
	})

	return body, res, nil
}

// isRemoteRef reports whether ref is a URL or data URI rather than a
// local-filesystem path.
func isRemoteRef(ref string) bool {
	lower := strings.ToLower(ref)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "//")
}

// uniqueFilename returns filename, possibly suffixed with `-N`, such that
// it does not collide with an existing entry in dir on fsys. Caps at 1000
// attempts to avoid pathological loops.
func uniqueFilename(fsys specio.FS, dir, filename string) string {
	if _, err := fsys.Stat(filepath.Join(dir, filename)); err != nil {
		return filename
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := fsys.Stat(filepath.Join(dir, candidate)); err != nil {
			return candidate
		}
	}
	return filename
}

// relRef returns the FS-relative path from mdDir to dest, suitable for
// embedding back into the markdown body. On error, falls back to dest.
func relRef(mdDir, dest string) string {
	rel, err := filepath.Rel(mdDir, dest)
	if err != nil {
		return dest
	}
	// Normalize to forward slashes so the rendered markdown is portable
	// across renderers and platforms.
	return filepath.ToSlash(rel)
}

