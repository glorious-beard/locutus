package spec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestApproachIsInvalidatedFalseByDefault(t *testing.T) {
	a := Approach{ID: "app-foo", Title: "Foo", ParentID: "feat-bar"}
	assert.False(t, a.IsInvalidated(),
		"a freshly constructed Approach must report IsInvalidated() == false")
}

func TestApproachIsInvalidatedTrueWhenEventIDPresent(t *testing.T) {
	a := Approach{
		ID:                   "app-foo",
		Title:                "Foo",
		ParentID:             "feat-bar",
		InvalidatedByEventID: "20260509T120953-001-node-superseded",
	}
	assert.True(t, a.IsInvalidated(),
		"an Approach with InvalidatedByEventID set must report IsInvalidated() == true")
}

func TestApproachIsInvalidatedTreatsWhitespaceAsEmpty(t *testing.T) {
	// A whitespace-only event id is meaningless — guard against a YAML
	// quirk producing it accidentally. Treat it the same as absent.
	a := Approach{
		ID:                   "app-foo",
		Title:                "Foo",
		ParentID:             "feat-bar",
		InvalidatedByEventID: "   \t\n",
	}
	assert.False(t, a.IsInvalidated(),
		"whitespace-only InvalidatedByEventID must be treated as not invalidated")
}

func TestApproachInvalidatedFieldYAMLRoundtrip(t *testing.T) {
	original := Approach{
		ID:                   "app-foo",
		Title:                "Foo approach",
		ParentID:             "feat-bar",
		InvalidatedByEventID: "20260509T120953-001-node-superseded",
	}
	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	// The on-disk key name is what tooling and external readers see;
	// pin it explicitly so a typo'd yaml tag doesn't slip through.
	assert.Contains(t, string(data), "invalidated_by_event_id:",
		"expected on-disk key invalidated_by_event_id, got: %s", string(data))

	var roundTripped Approach
	if err := yaml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	assert.Equal(t, original.InvalidatedByEventID, roundTripped.InvalidatedByEventID)
	assert.True(t, roundTripped.IsInvalidated())
}

func TestApproachInvalidatedFieldOmittedWhenEmpty(t *testing.T) {
	// omitempty keeps the YAML clean for the common case (most
	// approaches are valid). Explicitly verify the marshalled form
	// doesn't carry the key when the field is empty.
	a := Approach{ID: "app-foo", Title: "Foo", ParentID: "feat-bar"}
	data, err := yaml.Marshal(a)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	assert.NotContains(t, string(data), "invalidated_by_event_id",
		"valid approach must not emit the invalidation key, got: %s", string(data))
}

func TestApproach_SourceBindingFields(t *testing.T) {
	a := Approach{
		ID:                 "app-feat-foo",
		Title:              "Foo",
		ParentID:           "feat-foo",
		SourceFiles:        []string{"internal/foo/foo.go", "internal/foo/foo_test.go"},
		SourceHash:         "sha256:abc123",
		SourceHashSyncedAt: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
	out, err := yaml.Marshal(a)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	s := string(out)
	assert.Contains(t, s, "source_files:")
	assert.Contains(t, s, "internal/foo/foo.go")
	assert.Contains(t, s, "source_hash: sha256:abc123")
	assert.Contains(t, s, "source_hash_synced_at:")
}

func TestApproach_SourceBindingOmittedWhenEmpty(t *testing.T) {
	a := Approach{ID: "app-feat-bar", Title: "Bar", ParentID: "feat-bar"}
	out, err := yaml.Marshal(a)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	s := string(out)
	assert.NotContains(t, s, "source_files:")
	assert.NotContains(t, s, "source_hash:")
	assert.NotContains(t, s, "source_hash_synced_at:")
}
