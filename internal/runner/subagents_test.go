package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture: a tiny stream of SDK messages spanning two subagent
// dispatches. Shape mirrors what claude-agent-acp v0.33.1 emits
// per empirical smoke (see DJ-135 phase-5 follow-up).
//
// Wire facts the fixture exercises:
//   - Subagent dispatches appear as assistant messages whose
//     message.content[] carries a tool_use block with input.subagent_type
//     populated. The dispatch's tool_use.id is the group key.
//   - Downstream subagent messages carry parent_tool_use_id pointing
//     back to that dispatch.
//   - Orchestrator-level messages (no parent_tool_use_id and no
//     dispatching tool_use block) are skipped.
const sdkFixture = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu-scout","name":"Agent","input":{"subagent_type":"spec-scout","description":"survey the empty graph","prompt":"survey..."}}]}}
{"type":"user","parent_tool_use_id":"tu-scout","message":{"content":[{"type":"tool_result","tool_use_id":"tu-scout-1","content":"GOALS.md is empty"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu-decide","name":"Agent","input":{"subagent_type":"spec-decision-elaborator","description":"decide on hosting-runtime","prompt":"decide..."}}]}}
{"type":"result","subtype":"success","parent_tool_use_id":"tu-decide","usage":{"input_tokens":100}}
{"type":"assistant","message":{"content":[{"type":"text","text":"orchestrator-level message; no tool_use dispatching a subagent; should be skipped"}]}}
{"type":"user","parent_tool_use_id":"tu-orphan","message":{"content":[{"type":"tool_result","content":"orphan dispatch with no matching tool_use pre-scan; falls back to using parent_tool_use_id as the key"}]}}
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
	// Three groups: spec-scout (tu-scout, 2 messages — the dispatch +
	// the tool_result), spec-decision-elaborator (tu-decide, 2
	// messages), and the orphan parent_tool_use_id case (tu-orphan,
	// 1 message). The orchestrator-level message was skipped.
	if len(names) != 3 {
		t.Fatalf("expected 3 transcripts; got %d: %v", len(names), names)
	}
	if !strings.HasPrefix(names[0], "01-spec-scout") {
		t.Fatalf("first file should be 01-spec-scout.*; got %s", names[0])
	}
	if !strings.HasPrefix(names[1], "02-spec-decision-elaborator") {
		t.Fatalf("second file should be 02-spec-decision-elaborator.*; got %s", names[1])
	}
	// The orphan group has no dispatch info; should be 03-unknown.
	if !strings.HasPrefix(names[2], "03-unknown") {
		t.Fatalf("third file (orphan) should be 03-unknown.*; got %s", names[2])
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
		"**Group key:** `tu-scout`",
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
