package agent

import "fmt"

// defaultMaxChars is the threshold above which context is compacted.
// ~200K chars is roughly ~50K tokens — comfortably inside every modern
// model's context window (Gemini 3 / Claude 4.x / GPT-4o all run 200K+
// effective). The cap exists to guard against pathological growth
// (runaway model output looping into the next call's context), not to
// fit a tight 2K-token budget the way the original 8K cap did.
//
// Bumped from 8000 after the DJ-124 winplan validation surfaced a
// cascade: the scout-driven convergence loop accumulates decisions
// monotonically across iterations, so by iter-3 the assembled
// ProposedSpec exceeded 8K and projectChallenge truncated the
// critic's view to the first 8K chars — features and a couple of
// early decisions only. Critics then correctly reported "auth
// missing", "db hosting missing", etc. against the view they
// actually saw, but those decisions existed past the truncation
// cliff. Spurious findings accumulated; the scout's convergence
// rule ("axes_open empty AND no findings") never held; the loop
// budget-exhausted. The right structural fix is manifest-based
// in-flight projections (DJ-125 candidate); this cap bump is the
// immediate unblock for Phase 9 validation.
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
