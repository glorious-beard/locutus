package agent

import (
	"fmt"
	"path"
	"strings"

	"github.com/chetan/locutus/internal/frontmatter"
	"github.com/chetan/locutus/internal/specio"
)

// DefaultModel is the model used when an AgentDef has no explicit model.
const DefaultModel = "anthropic/claude-sonnet-4-20250514"

// AgentDef is a council agent definition loaded from a .md file.
type AgentDef struct {
	ID           string  `yaml:"id"`
	Role         string  `yaml:"role"`
	Model        string  `yaml:"model,omitempty"`
	Temperature  float64 `yaml:"temperature,omitempty"`
	SystemPrompt string  // the markdown body (not from YAML)
}

// LoadAgentDefs reads all .md files from the given directory on the FS.
// Each file has YAML frontmatter (id, role, model, temperature) and a
// markdown body which becomes the SystemPrompt.
func LoadAgentDefs(fsys specio.FS, dir string) ([]AgentDef, error) {
	// Check that the directory exists.
	info, err := fsys.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("agent dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("agent dir %q: not a directory", dir)
	}

	// List files in the directory. Use MemFS.ListDir if available, otherwise
	// fall back to os.ReadDir for OSFS.
	var paths []string
	if mem, ok := fsys.(*specio.MemFS); ok {
		paths = mem.ListDir(dir)
	} else {
		return nil, fmt.Errorf("unsupported FS type for listing directory")
	}

	var defs []AgentDef
	for _, p := range paths {
		if !strings.HasSuffix(path.Base(p), ".md") {
			continue
		}

		data, err := fsys.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading agent %q: %w", p, err)
		}

		var def AgentDef
		body, err := frontmatter.Parse(data, &def)
		if err != nil {
			return nil, fmt.Errorf("parsing agent %q: %w", p, err)
		}
		def.SystemPrompt = body

		defs = append(defs, def)
	}

	return defs, nil
}

// BuildGenerateRequest constructs a GenerateRequest from an AgentDef and
// user messages. The system prompt is prepended as a system-role message.
func BuildGenerateRequest(def AgentDef, messages []Message) GenerateRequest {
	model := def.Model
	if model == "" {
		model = DefaultModel
	}

	msgs := make([]Message, 0, len(messages)+1)
	msgs = append(msgs, Message{Role: "system", Content: def.SystemPrompt})
	msgs = append(msgs, messages...)

	return GenerateRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: def.Temperature,
	}
}
