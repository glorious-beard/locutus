package specio

import (
	"path"
	"sort"
	"strings"
)

// PairResult holds the result of loading a single spec pair.
type PairResult[T any] struct {
	Path   string // base path (without extension)
	Object T
	Body   string
	Err    error
}

// WalkPairs walks a directory, finds all .json files, and loads each as a pair
// (typed object T + markdown body). Results are returned in sorted order.
func WalkPairs[T any](fsys FS, dir string) ([]PairResult[T], error) {
	files, err := listFiles(fsys, dir)
	if err != nil {
		return nil, err
	}

	var results []PairResult[T]
	for _, f := range files {
		if path.Ext(f) != ".json" {
			continue
		}
		basePath := strings.TrimSuffix(f, ".json")
		obj, body, loadErr := LoadPair[T](fsys, basePath)
		results = append(results, PairResult[T]{
			Path:   basePath,
			Object: obj,
			Body:   body,
			Err:    loadErr,
		})
	}
	return results, nil
}

// FindOrphans finds .json files without matching .md files and vice versa in
// the given directory.
func FindOrphans(fsys FS, dir string) (jsonOnly []string, mdOnly []string, err error) {
	files, err := listFiles(fsys, dir)
	if err != nil {
		return nil, nil, err
	}

	jsonSet := make(map[string]bool)
	mdSet := make(map[string]bool)

	for _, f := range files {
		ext := path.Ext(f)
		base := strings.TrimSuffix(f, ext)
		switch ext {
		case ".json":
			jsonSet[base] = true
		case ".md":
			mdSet[base] = true
		}
	}

	for base := range jsonSet {
		if !mdSet[base] {
			jsonOnly = append(jsonOnly, base+".json")
		}
	}
	sort.Strings(jsonOnly)

	for base := range mdSet {
		if !jsonSet[base] {
			mdOnly = append(mdOnly, base+".md")
		}
	}
	sort.Strings(mdOnly)

	return jsonOnly, mdOnly, nil
}

// listFiles returns all file paths in a directory (non-recursive), sorted.
func listFiles(fsys FS, dir string) ([]string, error) {
	return fsys.ListDir(dir)
}
