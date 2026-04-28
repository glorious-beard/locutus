package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRoleContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", RoleFromContext(ctx))

	ctx = WithRole(ctx, "proposer")
	assert.Equal(t, "proposer", RoleFromContext(ctx))

	ctx = WithRole(ctx, "critic")
	assert.Equal(t, "critic", RoleFromContext(ctx),
		"WithRole should overwrite the prior role on the chain")
}

func TestSessionRecorderWritesToProjectFS(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "refine goals", "/test/project")
	require.NoError(t, err)
	require.NotEmpty(t, rec.SessionID())

	// Nested layout — .locutus/sessions/<YYYYMMDD>/<HHMMSS>/<short>.yaml.
	// Asserted via regex so we don't pin the test to whatever clock is
	// active at run time. The nesting matters because housekeeping
	// ("delete yesterday's logs") is `rm -rf` of one date directory.
	assert.Regexp(t,
		`^\.locutus/sessions/\d{8}/\d{6}/[0-9a-f]{6}\.yaml$`,
		rec.Path(),
		"session path must be nested by date and time so each day is one rm -rf away")

	data, err := fs.ReadFile(rec.Path())
	require.NoError(t, err)
	var session sessionFile
	require.NoError(t, yaml.Unmarshal(data, &session))
	assert.Equal(t, "refine goals", session.Command)
	assert.Equal(t, "/test/project", session.ProjectRoot)
	assert.Empty(t, session.Calls)
}

func TestSessionRecorderRecordsCallsInOrder(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	t1 := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	rec.Record("proposer",
		GenerateRequest{
			Model: "googleai/gemini-2.5-pro",
			Messages: []Message{
				{Role: "system", Content: "you are an architect"},
				{Role: "user", Content: "build it"},
			},
		},
		&GenerateResponse{Content: `{"features":[]}`, Model: "googleai/gemini-2.5-pro"},
		nil, t1, 1234*time.Millisecond,
	)
	rec.Record("critic",
		GenerateRequest{
			Model: "googleai/gemini-2.5-pro",
			Messages: []Message{
				{Role: "system", Content: "you are a critic"},
				{Role: "user", Content: "review this"},
			},
		},
		&GenerateResponse{Content: `{"issues":["x"]}`},
		nil, t1.Add(2*time.Second), 500*time.Millisecond,
	)

	data, err := fs.ReadFile(rec.Path())
	require.NoError(t, err)
	var session sessionFile
	require.NoError(t, yaml.Unmarshal(data, &session))

	require.Len(t, session.Calls, 2)
	assert.Equal(t, 1, session.Calls[0].Index)
	assert.Equal(t, "proposer", session.Calls[0].Role)
	assert.Equal(t, int64(1234), session.Calls[0].DurationMS)
	assert.Equal(t, 2, session.Calls[1].Index)
	assert.Equal(t, "critic", session.Calls[1].Role)
}

func TestSessionRecorderEmitsLiteralBlocksForMultilineContent(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer",
		GenerateRequest{
			Model: "test-model",
			Messages: []Message{
				{Role: "system", Content: "rule one\nrule two\nrule three"},
				{Role: "user", Content: "## Header\n\nbody line\n"},
			},
		},
		&GenerateResponse{Content: `{"k":"v"}`},
		nil, time.Now(), 0,
	)

	raw, err := fs.ReadFile(rec.Path())
	require.NoError(t, err)
	out := string(raw)

	// yaml.v3 picks block scalar styles for multiline strings so the
	// transcript reads as the original prose. The multi-line system
	// rule should land under a `|` (or `|-`) literal indicator, not as
	// a quoted "rule one\nrule two\nrule three".
	assert.Regexp(t, `content: \|-?\n\s+rule one\n\s+rule two\n\s+rule three`, out,
		"expected literal block for multiline system content")
	assert.NotContains(t, out, `"rule one\nrule two\nrule three"`,
		"multiline content must not collapse to an escaped single-line string")
}

func TestSessionRecorderRecordsErrors(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	rec.Record("proposer",
		GenerateRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}},
		nil,
		fmt.Errorf("model unavailable"),
		time.Now(), 0,
	)

	raw, _ := fs.ReadFile(rec.Path())
	var session sessionFile
	require.NoError(t, yaml.Unmarshal(raw, &session))
	require.Len(t, session.Calls, 1)
	assert.Equal(t, "model unavailable", session.Calls[0].Error)
	assert.Empty(t, session.Calls[0].Response)
}

func TestLoggingLLMRecordsAndDelegates(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: `{"ok":true}`, Model: "test-model"},
	})
	logging := NewLoggingLLM(mock, rec)

	ctx := WithRole(context.Background(), "proposer")
	resp, err := logging.Generate(ctx, GenerateRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, resp.Content)
	assert.Equal(t, 1, mock.CallCount(), "inner LLM should still be called exactly once")

	raw, _ := fs.ReadFile(rec.Path())
	var session sessionFile
	require.NoError(t, yaml.Unmarshal(raw, &session))
	require.Len(t, session.Calls, 1)
	assert.Equal(t, "proposer", session.Calls[0].Role,
		"role from context should land on the recorded call")
	assert.Equal(t, `{"ok":true}`, session.Calls[0].Response)
}

func TestLoggingLLMRecordsErrorPath(t *testing.T) {
	fs := specio.NewMemFS()
	rec, err := NewSessionRecorder(fs, "test", "")
	require.NoError(t, err)

	mock := NewMockLLM(MockResponse{Err: fmt.Errorf("rate limited")})
	logging := NewLoggingLLM(mock, rec)

	_, err = logging.Generate(context.Background(), GenerateRequest{
		Model: "m", Messages: []Message{{Role: "user", Content: "x"}},
	})
	require.Error(t, err)

	raw, _ := fs.ReadFile(rec.Path())
	var session sessionFile
	require.NoError(t, yaml.Unmarshal(raw, &session))
	require.Len(t, session.Calls, 1)
	assert.Equal(t, "rate limited", session.Calls[0].Error)
}
