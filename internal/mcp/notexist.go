package mcp

import (
	"errors"
	"io/fs"
	"strings"
)

// isPlanFileNotExist matches both fs.ErrNotExist (OSFS via
// *os.PathError) and the MemFS "file does not exist" string sentinel
// that doesn't unwrap to the stdlib sentinel. Duplicated from
// internal/publisher/canonical.go's same-named helper because pulling
// it across packages would require an exported helper that both
// callers route through; one-line copies are cheaper.
func isPlanFileNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "file does not exist")
}
