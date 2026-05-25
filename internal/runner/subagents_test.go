package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture: a tiny stream of SDK messages spanning two subagent
// dispatches plus an orchestrator-level message that should be
// filtered out.
const sdkFixture = `{"type":"assistant","subagent_type":"spec-scout","request_id":"req-aaa","task_description":"survey the empty graph","message":"i'll start by..."}
{"type":"user","subagent_type":"spec-scout","request_id":"req-aaa","content":[{"type":"tool_result","tool_use_id":"tu1","content":"GOALS.md is empty"}]}
{"type":"assistant","subagent_type":"spec-decision-elaborator","request_id":"req-bbb","task_description":"decide on hosting-runtime","content":"choosing fly.io because..."}
{"type":"result","subtype":"success","subagent_type":"spec-decision-elaborator","request_id":"req-bbb","usage":{"input_tokens":100}}
{"type":"assistant","content":"orchestrator-level message; no subagent_type or parent_tool_use_id; should be skipped"}
{"type":"user","parent_tool_use_id":"tu-fallback","content":"falls back to parent_tool_use_id grouping"}
`

func TestExtractSubagentTranscripts_GroupsByRequestID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sdk-messages.jsonl"), []byte(sdkFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractSubagentTranscripts(dir); err != nil {
		t.Fatalf("extractSubagentTranscripts err: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "subagents"))
	if err != nil {
		t.Fatalf("subagents/ dir missing: %v", err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	// Three groups: spec-scout (req-aaa), spec-decision-elaborator
	// (req-bbb), the synthetic parent_tool_use_id one. The
	// orchestrator-level message was skipped.
	if len(names) != 3 {
		t.Fatalf("expected 3 transcripts; got %d: %v", len(names), names)
	}
	// File numbering follows dispatch order: 01-spec-scout, 02-spec-decision-elaborator, ...
	if !strings.HasPrefix(names[0], "01-spec-scout") {
		t.Fatalf("first file should be 01-spec-scout.*; got %s", names[0])
	}
	if !strings.HasPrefix(names[1], "02-spec-decision-elaborator") {
		t.Fatalf("second file should be 02-spec-decision-elaborator.*; got %s", names[1])
	}
}

func TestExtractSubagentTranscripts_HeaderCarriesTaskAndType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sdk-messages.jsonl"), []byte(sdkFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractSubagentTranscripts(dir); err != nil {
		t.Fatalf("extractSubagentTranscripts err: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "subagents", "01-spec-scout.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"# Subagent transcript: spec-scout",
		"**Task:** survey the empty graph",
		"**Group key:** `req-aaa`",
		"## 01. assistant",
		"## 02. user",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("transcript missing %q\n--- transcript ---\n%s", want, s)
		}
	}
}

func TestExtractSubagentTranscripts_MissingFileIsSilentNoOp(t *testing.T) {
	// Codex / Gemini don't emit sdkMessage notifications. The
	// extractor must not error when the file isn't present.
	dir := t.TempDir()
	if err := extractSubagentTranscripts(dir); err != nil {
		t.Fatalf("missing sdk-messages.jsonl should be silent; got err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subagents")); !os.IsNotExist(err) {
		t.Fatalf("subagents/ dir should not be created when no input; stat err: %v", err)
	}
}

func TestExtractSubagentTranscripts_MalformedLinesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	mixed := "not-json\n" + sdkFixture + "{another bad line"
	if err := os.WriteFile(filepath.Join(dir, "sdk-messages.jsonl"), []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractSubagentTranscripts(dir); err != nil {
		t.Fatalf("malformed lines should be skipped silently; got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "subagents"))
	if len(entries) != 3 {
		t.Fatalf("malformed lines should be tolerated; got %d transcripts", len(entries))
	}
}

func TestSanitize_FilenameSafety(t *testing.T) {
	cases := []struct{ in, want string }{
		{"spec-scout", "spec-scout"},
		{"spec_decision_elaborator", "spec_decision_elaborator"},
		{"weird/path:thing", "weird-path-thing"},
		{"", "unknown"},
		{"   ", "unknown"},
		{"!!!", "unknown"},
	}
	for _, tc := range cases {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
