package cmd

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

// TestImportFeature verifies that ImportFeature parses markdown with YAML
// frontmatter, creates a Feature with correct fields, sets status to "proposed",
// and writes the .json and .md pair to .borg/spec/features/.
func TestImportFeature(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)

	input := []byte(`---
id: feat-auth
title: User Authentication
type: feature
---

Users should be able to log in with email and password.
`)

	feat, err := ImportFeature(fs, input)
	assert.NoError(t, err)
	if !assert.NotNil(t, feat) {
		return
	}

	// Verify returned Feature fields.
	assert.Equal(t, "feat-auth", feat.ID)
	assert.Equal(t, "User Authentication", feat.Title)
	assert.Equal(t, spec.FeatureStatusProposed, feat.Status, "imported features should default to 'proposed'")

	// Verify files were written to the MemFS.
	jsonData, err := fs.ReadFile(".borg/spec/features/feat-auth.json")
	assert.NoError(t, err, ".json file should exist in .borg/spec/features/")
	assert.NotEmpty(t, jsonData)

	mdData, err := fs.ReadFile(".borg/spec/features/feat-auth.md")
	assert.NoError(t, err, ".md file should exist in .borg/spec/features/")
	assert.NotEmpty(t, mdData)
}

// TestImportBug verifies that ImportBug parses markdown with YAML frontmatter
// containing type=bug, severity, and feature_id, creates a Bug with correct
// fields, and sets status to "reported".
func TestImportBug(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/bugs", 0o755)

	input := []byte(`---
id: bug-login-crash
title: Login crashes on empty password
type: bug
severity: high
feature_id: feat-auth
---

When a user submits the login form with an empty password field, the
application crashes with a nil pointer dereference.
`)

	bug, err := ImportBug(fs, input)
	assert.NoError(t, err)
	if !assert.NotNil(t, bug) {
		return
	}

	// Verify returned Bug fields.
	assert.Equal(t, "bug-login-crash", bug.ID)
	assert.Equal(t, "Login crashes on empty password", bug.Title)
	assert.Equal(t, spec.BugStatusReported, bug.Status, "imported bugs should default to 'reported'")
	assert.Equal(t, spec.BugSeverityHigh, bug.Severity)
	assert.Equal(t, "feat-auth", bug.FeatureID)

	// Verify files were written to the MemFS.
	jsonData, err := fs.ReadFile(".borg/spec/bugs/bug-login-crash.json")
	assert.NoError(t, err, ".json file should exist in .borg/spec/bugs/")
	assert.NotEmpty(t, jsonData)

	mdData, err := fs.ReadFile(".borg/spec/bugs/bug-login-crash.md")
	assert.NoError(t, err, ".md file should exist in .borg/spec/bugs/")
	assert.NotEmpty(t, mdData)
}

// TestImportInvalidInput verifies that ImportFeature returns an error when
// given markdown without valid YAML frontmatter.
func TestImportInvalidInput(t *testing.T) {
	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)

	// No frontmatter delimiters at all.
	input := []byte(`This is just plain markdown without any frontmatter.

It should not be importable as a feature.
`)

	feat, err := ImportFeature(fs, input)
	assert.Error(t, err, "should return error for input without frontmatter")
	assert.Nil(t, feat)
}
