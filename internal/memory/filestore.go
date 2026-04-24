package memory

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"

	"github.com/chetan/locutus/internal/specio"
	"gopkg.in/yaml.v3"
)

// FileStoreService persists entries as one YAML file per entry at
// <root>/<namespace>/<id>.yaml. Safe for concurrent use within a single
// process; not coordinated across processes.
type FileStoreService struct {
	mu   sync.Mutex
	fsys specio.FS
	root string
}

// NewFileStoreService returns a file-backed memory service rooted at the
// given directory within fsys. Directories are created lazily on first
// write.
func NewFileStoreService(fsys specio.FS, root string) *FileStoreService {
	return &FileStoreService{fsys: fsys, root: root}
}

// AddSessionToMemory persists each entry as <root>/<namespace>/<id>.yaml.
func (s *FileStoreService) AddSessionToMemory(ctx context.Context, namespace string, entries []Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareEntries(namespace, entries)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nsDir := path.Join(s.root, namespace)
	if err := s.fsys.MkdirAll(nsDir, 0o755); err != nil {
		return fmt.Errorf("memory add mkdir %q: %w", nsDir, err)
	}
	for _, e := range prepared {
		data, err := yaml.Marshal(e)
		if err != nil {
			return fmt.Errorf("memory add marshal %q: %w", e.ID, err)
		}
		p := path.Join(nsDir, e.ID+".yaml")
		if err := s.fsys.WriteFile(p, data, 0o644); err != nil {
			return fmt.Errorf("memory add write %q: %w", p, err)
		}
	}
	return nil
}

// SearchMemory walks matching namespaces and returns matching entries.
// Corrupt YAML files are logged and skipped — a bad entry must not break
// the rest of the namespace.
func (s *FileStoreService) SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("memory: nil search request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var namespaces []string
	if req.Namespace == "" {
		subs, err := s.fsys.ListSubdirs(s.root)
		if err == nil {
			for _, sub := range subs {
				namespaces = append(namespaces, path.Base(sub))
			}
		}
	} else {
		namespaces = []string{req.Namespace}
	}

	var all []Entry
	for _, ns := range namespaces {
		entries := s.readNamespace(ns)
		all = append(all, filterEntries(entries, req.Query)...)
	}
	sortNewestFirst(all)
	if req.Limit > 0 && len(all) > req.Limit {
		all = all[:req.Limit]
	}
	return &SearchResponse{Entries: all}, nil
}

func (s *FileStoreService) readNamespace(ns string) []Entry {
	nsDir := path.Join(s.root, ns)
	files, err := s.fsys.ListDir(nsDir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, p := range files {
		if !strings.HasSuffix(p, ".yaml") {
			continue
		}
		data, err := s.fsys.ReadFile(p)
		if err != nil {
			slog.Warn("memory: skipping unreadable entry", "path", p, "err", err)
			continue
		}
		var e Entry
		if err := yaml.Unmarshal(data, &e); err != nil {
			slog.Warn("memory: skipping corrupt entry", "path", p, "err", err)
			continue
		}
		out = append(out, e)
	}
	return out
}
