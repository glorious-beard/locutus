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

func TestCLIParsesVersion(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("locutus"), kong.Exit(func(int) {}))
	assert.NoError(t, err)

	ctx, err := parser.Parse([]string{"version"})
	assert.NoError(t, err)
	assert.Equal(t, "version", ctx.Command())
}

func TestVersionRunPlain(t *testing.T) {
	cli := CLI{JSON: false}
	cmd := VersionCmd{Version: "1.0.0"}

	out := captureStdout(func() {
		err := cmd.Run(&cli)
		assert.NoError(t, err)
	})

	assert.Contains(t, out, "locutus 1.0.0")
}

func TestVersionRunJSON(t *testing.T) {
	cli := CLI{JSON: true}
	cmd := VersionCmd{Version: "1.0.0"}

	out := captureStdout(func() {
		err := cmd.Run(&cli)
		assert.NoError(t, err)
	})

	assert.Contains(t, out, `"version":"1.0.0"`)
}
