//go:build !windows

package specio

import (
	"os"

	"github.com/google/renameio/v2"
)

// atomicWriteFile delegates to renameio/v2 on Linux/macOS. The package
// writes to a same-directory temp file, fsyncs it, renames to the
// target path, and fsyncs the containing directory — the directory
// fsync is the load-bearing step on ext4/xfs, where the metadata
// journal can otherwise leave the rename buffered after we've returned.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return renameio.WriteFile(path, data, perm)
}
