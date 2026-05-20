package agent

import "fmt"

// defaultMaxChars is the threshold above which context is compacted.
// ~200K chars is roughly ~50K tokens — comfortably inside every modern
// model's context window (Gemini 3 / Claude 4.x / GPT-4o all run 200K+
// effective). The cap exists to guard against pathological growth
// (runaway model output looping into the next call's context), not to
// fit a tight 2K-token budget the way the original 8K cap did.
//
// DJ-125 Phase 8 update: this cap is no longer load-bearing on the
// hot spec-generation council paths — projectChallenge,
// projectReconcile, projectScout, projectOpenAxis, and
// projectAffectedNode all render the in-flight manifest (compact text
// rendering) instead of dumping ProposedSpec / RawProposal verbatim.
// The remaining compactContext callers live in convergence.go's
// legacy path (DJ-122 superseded for spec-gen; preserved for older
// non-council convergence flows). The cap stays at 200K as
// defense-in-depth on those legacy paths and any future projection
// that renders large bodies; the manifest replaces it as the primary
// projection-size lever on the council path.
//
// History: bumped from 8000 after the DJ-124 winplan validation
// surfaced a cascade — the scout-driven convergence loop accumulates
// decisions monotonically across iterations, so by iter-3 the
// assembled ProposedSpec exceeded 8K and projectChallenge truncated
// the critic's view to the first 8K chars. Critics then correctly
// reported "auth missing" against the view they actually saw, but
// those decisions existed past the truncation cliff. The structural
// fix was manifest-based in-flight projections (DJ-125 Phase 4); this
// cap is now a safety net, not a load-bearing constraint.
const defaultMaxChars = 200000

// compactContext truncates content that exceeds maxChars, appending a
// count summary. This prevents context window blowout on large projects.
// If content is within the limit, it's returned unchanged.
func compactContext(content string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if len(content) <= maxChars {
		return content
	}
	truncated := content[:maxChars]
	remaining := len(content) - maxChars
	return truncated + fmt.Sprintf("\n\n... (%d more characters truncated)\n", remaining)
}
