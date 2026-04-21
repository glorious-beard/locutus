package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
)

// captureStdout redirects os.Stdout to a pipe, runs fn, and returns whatever
// was written to stdout as a string.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	return string(out)
}

// TestCLIParsesStatus checks the Kong parser recognises one of the
// canonical verbs. Phase A dropped the `version` subcommand in favour of
// the `--version` flag, so we assert against `status` instead.
func TestCLIParsesStatus(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("locutus"),
		kong.Vars{"version": "test"},
		kong.Exit(func(int) {}),
	)
	assert.NoError(t, err)

	ctx, err := parser.Parse([]string{"status"})
	assert.NoError(t, err)
	assert.Equal(t, "status", ctx.Command())
}

// TestSetVersion exercises the buildVersion helper used by `update`.
func TestSetVersion(t *testing.T) {
	orig := buildVersion
	defer func() { buildVersion = orig }()

	SetVersion("1.2.3")
	assert.Equal(t, "1.2.3", buildVersion)

	SetVersion("") // empty input is ignored
	assert.Equal(t, "1.2.3", buildVersion)
}
