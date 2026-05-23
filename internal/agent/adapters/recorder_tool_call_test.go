package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeToolCallHandle captures the Finish callback so the test can
// assert what the dispatch helper recorded.
type fakeToolCallHandle struct {
	finished bool
	output   []byte
	err      error
}

func (f *fakeToolCallHandle) Finish(output []byte, err error) {
	f.finished = true
	f.output = output
	f.err = err
}

// fakeCallHandle implements CallHandle, capturing BeginToolCall calls
// so the test can verify recorder integration.
type fakeCallHandle struct {
	begins []struct {
		name   string
		callID string
		input  []byte
	}
	toolHandles []*fakeToolCallHandle
}

func (f *fakeCallHandle) Finish(*Response, error) {}

func (f *fakeCallHandle) BeginToolCall(_ context.Context, name, callID string, input []byte, _ time.Time) ToolCallHandle {
	f.begins = append(f.begins, struct {
		name   string
		callID string
		input  []byte
	}{name, callID, input})
	h := &fakeToolCallHandle{}
	f.toolHandles = append(f.toolHandles, h)
	return h
}

// TestRunRecordedTool_SuccessCapturesIO confirms a successful handler
// run flows through the recorder: BeginToolCall fires with the input
// before the handler runs; the returned ToolCallHandle's Finish
// receives the handler's output + nil error.
func TestRunRecordedTool_SuccessCapturesIO(t *testing.T) {
	want := []byte(`{"ok": true}`)
	def := ToolDef{
		Name: "spec_get",
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			assert.JSONEq(t, `{"ids":["dec-foo"]}`, string(input))
			return want, nil
		},
	}
	handle := &fakeCallHandle{}
	got, err := runRecordedTool(context.Background(), handle, def, "tooluse_01", []byte(`{"ids":["dec-foo"]}`))
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.Len(t, handle.begins, 1, "BeginToolCall must fire exactly once")
	assert.Equal(t, "spec_get", handle.begins[0].name)
	assert.Equal(t, "tooluse_01", handle.begins[0].callID)
	assert.JSONEq(t, `{"ids":["dec-foo"]}`, string(handle.begins[0].input))

	require.Len(t, handle.toolHandles, 1)
	th := handle.toolHandles[0]
	assert.True(t, th.finished, "ToolCallHandle.Finish must fire")
	assert.Equal(t, want, th.output, "output passed to Finish must be the handler's return value")
	assert.NoError(t, th.err)
}

// TestRunRecordedTool_ErrorCapturesErr confirms a handler error
// propagates through to ToolCallHandle.Finish so the per-call YAML
// records the failure rather than silently dropping it.
func TestRunRecordedTool_ErrorCapturesErr(t *testing.T) {
	wantErr := errors.New("simulated handler failure")
	def := ToolDef{
		Name:    "spec_get",
		Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, wantErr },
	}
	handle := &fakeCallHandle{}
	_, err := runRecordedTool(context.Background(), handle, def, "tooluse_02", []byte(`{"ids":[]}`))
	require.Error(t, err)
	assert.Equal(t, wantErr, err)

	require.Len(t, handle.toolHandles, 1)
	th := handle.toolHandles[0]
	assert.True(t, th.finished)
	assert.Equal(t, wantErr, th.err, "handler error must reach Finish")
	assert.Nil(t, th.output, "no output on error path")
}

// TestRunRecordedTool_NilHandleIsNoop confirms a nil recorder handle
// doesn't panic and the handler still runs. Mirrors the production
// contract for ad-hoc adapter callers (tests, MCP raw calls) that
// don't set a recorder on context.
func TestRunRecordedTool_NilHandleIsNoop(t *testing.T) {
	called := false
	def := ToolDef{
		Name:    "spec_search",
		Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { called = true; return json.RawMessage("{}"), nil },
	}
	out, err := runRecordedTool(context.Background(), nil, def, "x", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("{}"), out)
	assert.True(t, called, "handler must run even when no recorder is wired")
}
