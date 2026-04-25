// Package memory provides a namespaced store for agent learned context.
//
// It is a third persistence class, distinct from spec/state files (DJ-068,
// git-tracked) and workstream coordination records (DJ-073, transient).
// Where those are respectively the user's goal graph and in-flight dispatch
// state, this package holds knowledge curated by one agent for future
// agents to consult, keyed by namespace.
//
// Scoping: by namespace (e.g. "archivist", "planner", "project"). Locutus
// is single-user and single-project, so we do not mirror ADK's user/app
// scoping. Cross-namespace queries are allowed by passing an empty
// Namespace on SearchRequest.
//
// Search: case-insensitive substring match over Entry.Content. The
// Service shape leaves room for an embedding-backed implementation later.
//
// Portions adapted from github.com/google/adk-go/memory (two-method
// Service interface with AddSessionToMemory + SearchMemory, Entry shape).
// Copyright 2025 Google LLC. Licensed under the Apache License 2.0. See
// NOTICE at the repo root for attribution. Per DJ-077.
package memory

import (
	"context"
	"errors"
	"time"
)

// Entry is a single memory record. The store populates ID, Namespace, and
// Timestamp on Add when they are left zero; callers typically supply only
// Content, Author, and CustomMetadata.
type Entry struct {
	ID             string         `yaml:"id"`
	Namespace      string         `yaml:"namespace"`
	Content        string         `yaml:"content"`
	Author         string         `yaml:"author,omitempty"`
	Timestamp      time.Time      `yaml:"timestamp"`
	CustomMetadata map[string]any `yaml:"custom_metadata,omitempty"`
}

// Service is the memory store.
//
// AddSessionToMemory ingests a batch of entries under the given namespace.
// Each entry is assigned a fresh UUID if its ID is empty, stamped with the
// current time if its Timestamp is zero, and bound to the batch namespace.
// Passing an entry whose Namespace is set and differs from the batch
// namespace is an error.
//
// SearchMemory filters entries by namespace (empty = cross-namespace) and
// by case-insensitive substring match against Entry.Content. Results are
// ordered newest-first and capped by Limit (0 = unlimited). Empty Query
// matches every entry in scope.
type Service interface {
	AddSessionToMemory(ctx context.Context, namespace string, entries []Entry) error
	SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// SearchRequest scopes and filters a memory lookup.
type SearchRequest struct {
	Namespace string
	Query     string
	Limit     int
}

// SearchResponse carries matched entries, newest first.
type SearchResponse struct {
	Entries []Entry
}

// ErrInvalidEntry signals a batch rejected before any write: empty content,
// or a per-entry namespace that conflicts with the batch namespace.
var ErrInvalidEntry = errors.New("memory: invalid entry")
