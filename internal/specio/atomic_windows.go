//go:build windows

package specio

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile is the Windows-side counterpart to the Unix renameio
// path. renameio/v2 deliberately excludes Windows because POSIX-style
// rename-on-top-of-existing-file semantics differ; we provide an
// equivalent via os.CreateTemp + os.Rename, which on Go's Windows
// runtime maps to MoveFileEx with MOVEFILE_REPLACE_EXISTING. NTFS
// journaling provides the durability guarantee that's analogous to
// the directory-fsync step renameio performs on Linux.
//
// Sequence:
//
//  1. Create a temp file in the same directory as the target so the
//     rename stays on a single filesystem (cross-volume renames are
//     a copy on Windows and break atomicity).
//  2. Write data, fsync, close.
//  3. Set the requested mode (CreateTemp ignores perm on Windows).
//  4. Rename to target. If the rename fails, remove the temp so we
//     don't leak detritus next to the target.
//
// On any error before the rename succeeds, the target file is left
// at its prior content. On a process kill mid-write, the temp file
// is what's incomplete; the target still holds the prior version.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomic write %s: create temp: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomic write %s: write: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomic write %s: sync: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("atomic write %s: close: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("atomic write %s: chmod: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("atomic write %s: rename: %w", path, err)
	}
	return nil
}
