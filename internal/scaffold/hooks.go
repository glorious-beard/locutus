package scaffold

import (
	"embed"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// hooksFS embeds the per-runtime hook configs for activities that
// have mechanical enforcement (DJ-136 phase 5+). Codex uses TOML
// fragments appended to .codex/config.toml; Gemini uses JSON objects
// inserted into .gemini/settings.json's hooks array. The shape per
// runtime is the runtime's contract — Locutus only emits the
// fragments and templates the binary path.
//
// Path convention: hooks/<runtime>/<activity>.<ext>
//
//go:embed hooks/codex/*.toml hooks/gemini/*.json
var hooksFS embed.FS

// ReadEmbeddedHook returns the embedded hook fragment for the
// runtime + activity pair, with the {{LOCUTUS_BIN}} placeholder
// substituted to the given binary path. Returns (nil, false, nil)
// when no hook fragment is registered for that runtime+activity —
// the common case for runtimes that don't need mechanical
// enforcement on a particular activity.
func ReadEmbeddedHook(runtime, activityName, locutusBin string) ([]byte, bool, error) {
	candidates := hookCandidates(runtime, activityName)
	for _, p := range candidates {
		data, err := hooksFS.ReadFile(p)
		if err == nil {
			return []byte(strings.ReplaceAll(string(data), "{{LOCUTUS_BIN}}", locutusBin)), true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
	}
	return nil, false, nil
}

// HookActivities returns the list of activities that have a hook
// fragment registered for the given runtime. Used by the
// publisher's hook emit step to iterate the canonical set without
// needing the activity registry's load order.
func HookActivities(runtime string) []string {
	dir := "hooks/" + runtime
	entries, err := hooksFS.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := path.Ext(name)
		out = append(out, strings.TrimSuffix(name, ext))
	}
	sort.Strings(out)
	return out
}

func hookCandidates(runtime, activityName string) []string {
	// Per-runtime expected extensions. Encoded inline rather than
	// in a map so the function stays trivially auditable; the
	// extension set is small and stable.
	switch runtime {
	case "codex":
		return []string{"hooks/codex/" + activityName + ".toml"}
	case "gemini":
		return []string{"hooks/gemini/" + activityName + ".json"}
	}
	return nil
}

