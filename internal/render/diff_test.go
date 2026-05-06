package render

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderDiffEmptyOnIdentical(t *testing.T) {
	out := RenderDiff("a", "b", "same\ntext\n", "same\ntext\n")
	assert.Empty(t, out, "identical inputs produce no diff")
}

func TestRenderDiffAddedLine(t *testing.T) {
	old := "alpha\nbeta\n"
	newer := "alpha\nbeta\ngamma\n"
	out := RenderDiff("before", "after", old, newer)
	assert.Contains(t, out, "--- before")
	assert.Contains(t, out, "+++ after")
	assert.Contains(t, out, "+gamma")
	assert.NotContains(t, out, "-alpha", "unchanged lines are not deletions")
}

func TestRenderDiffRemovedLine(t *testing.T) {
	old := "alpha\nbeta\ngamma\n"
	newer := "alpha\ngamma\n"
	out := RenderDiff("a", "b", old, newer)
	assert.Contains(t, out, "-beta")
}

func TestRenderDiffReplacedParagraph(t *testing.T) {
	old := "title\n\nold body line one\nold body line two\n"
	newer := "title\n\nnew body line one\nnew body line two\n"
	out := RenderDiff("a", "b", old, newer)
	assert.Contains(t, out, "-old body line one")
	assert.Contains(t, out, "+new body line one")
	assert.Contains(t, out, " title", "unchanged title preserved as context")
}

func TestRenderDiffEmptyOldAdded(t *testing.T) {
	out := RenderDiff("a", "b", "", "first\nsecond\n")
	assert.Contains(t, out, "+first")
	assert.Contains(t, out, "+second")
}

func TestRenderDiffEmptyNewRemoved(t *testing.T) {
	out := RenderDiff("a", "b", "first\nsecond\n", "")
	assert.Contains(t, out, "-first")
	assert.Contains(t, out, "-second")
}

func TestRenderDiffHunkHeader(t *testing.T) {
	old := "a\nb\nc\nd\ne\n"
	newer := "a\nb\nC-changed\nd\ne\n"
	out := RenderDiff("a", "b", old, newer)
	// One change in the middle → one hunk with surrounding context.
	hunkHeaders := strings.Count(out, "@@ ")
	assert.Equal(t, 1, hunkHeaders, "single-change diff has exactly one hunk")
	assert.Contains(t, out, "-c")
	assert.Contains(t, out, "+C-changed")
}
