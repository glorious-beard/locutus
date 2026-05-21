// DJ-129 — the four fixed critic agent files are retired in favor of
// the parametric spec_critic_elaborator. Their lens-specific content
// migrated to the discipline sections of spec_critic_elaborator.md.

package agents_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetiredCriticAgentsAbsentFromScaffold(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"architect_critic.md",
		"devops_critic.md",
		"sre_critic.md",
		"cost_critic.md",
	} {
		path := filepath.Join(wd, name)
		_, err := os.Stat(path)
		assert.True(t, os.IsNotExist(err), "DJ-129 retired %s — file should be deleted", name)
	}
}
