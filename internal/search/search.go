// Package search provides a persistent BM25 full-text index over the
// spec graph stored under .borg/spec/. The index lives at
// .locutus/spec_index/ (gitignored, regenerable) and is built on top
// of Bluge — a pure-Go, CGo-free FTS engine that preserves Locutus's
// static-binary property.
//
// Phase 1 ships the foundation: build, open (with fingerprint
// invalidation), search, and writer-with-retry. No CLI verb consumes
// it yet — that arrives in subsequent phases.
//
// The public surface is small on purpose:
//
//   - Open(fsys, projectRoot) opens the on-disk index, validating
//     schema-version and fingerprint and rebuilding on mismatch.
//   - OpenInMemory(fsys) builds a fresh in-memory index, intended
//     for tests and one-off introspection.
//   - (*Index).Search runs a query and returns ranked hits.
//   - (*Index).Update / (*Index).Delete are reserved for the
//     mutation-hook integration that lands in Phase 2.
//
// The package depends on internal/specio (FS interface) and reads
// .borg/spec/ directly via specio.WalkPairs rather than going through
// internal/spec.LoadSpec — avoids loading the whole graph just to
// produce a flat indexable view, and keeps the dependency surface
// narrow.
package search

import "errors"

// SchemaVersion is bumped manually whenever the field mapping in
// build.go changes. The on-disk fingerprint embeds this value so any
// upgrade that changes the indexed shape triggers a rebuild rather
// than silently producing inconsistent ranking.
const SchemaVersion = 1

// IndexDir is the on-disk location of the persisted index, relative
// to the project root. Gitignored; safe to `rm -rf` as an operator
// escape hatch.
const IndexDir = ".locutus/spec_index"

// fingerprintFile names the small text file that stores the
// (schema-version, mtime-hash) tuple alongside the Bluge segments.
// Lives inside IndexDir so a single `rm -rf .locutus/spec_index/`
// resets everything.
const fingerprintFile = "fingerprint"

// DefaultLimit caps the number of returned hits when the caller
// doesn't override it. 100 matches the operator-facing list verb's
// natural ceiling — the agent-facing spec_search tool clamps at the
// same value.
const DefaultLimit = 100

// Hit is one search match. Score is opaque relevance — order is the
// API, not the magnitude. Kind is the spec node kind (feature,
// strategy, decision, bug, approach); Title is the curated headline.
type Hit struct {
	ID    string
	Kind  string
	Title string
	Score float64
}

// Options configures a single Search call.
//
// Kind, when non-empty, restricts results to one node kind via a
// keyword filter (AND'd with the free-text scoring query). Accepted
// values: "feature", "strategy", "decision", "bug", "approach".
//
// Limit caps returned hits; 0 means DefaultLimit. Bluge always
// produces ranked top-N results, so a small Limit is cheap.
type Options struct {
	Kind  string
	Limit int
}

// ErrEmptyQuery is returned by Search when the caller passes a query
// that tokenises to nothing (empty or whitespace-only).
var ErrEmptyQuery = errors.New("search: query is empty")

// ErrUnknownKind is returned by Search when Options.Kind names a
// value outside the accepted set.
var ErrUnknownKind = errors.New("search: unknown kind filter")
