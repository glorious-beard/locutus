package agent

import "fmt"

// defaultMaxChars is the threshold above which context is compacted.
// ~8K chars is roughly ~2K tokens.
const defaultMaxChars = 8000

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
