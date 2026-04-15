package spec

// MCPResponse is the universal structured return type for all commands.
type MCPResponse struct {
	Status      string       `json:"status" yaml:"status"`
	Data        any          `json:"data,omitempty" yaml:"data,omitempty"`
	Errors      []string     `json:"errors,omitempty" yaml:"errors,omitempty"`
	FileChanges []FileChange `json:"file_changes,omitempty" yaml:"file_changes,omitempty"`
}

// FileChange describes a single file mutation within a response.
type FileChange struct {
	Path        string `json:"path" yaml:"path"`
	Action      string `json:"action" yaml:"action"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}
