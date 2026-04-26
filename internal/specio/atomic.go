package specio

import (
	"os"

	"github.com/google/renameio/v2"
)

// AtomicWriteFile writes data to name atomically when fsys is OS-backed: the
// data lands in a same-directory temp file and is then renamed into place,
// so a SIGKILL or power loss between the open and the rename leaves the
// destination either at its prior content or at the new content — never
// truncated. The OS-path delegates to renameio/v2, which also fsyncs the
// containing directory after rename so ext4 / xfs metadata-journal flushes
// land before we declare success. For non-OSFS implementations (notably
// MemFS) the write goes straight through, since those have no on-disk
// crash window.
//
// Per DJ-068 (state store) and DJ-073 (workstream records), every persisted
// reconcile/dispatch artifact must survive a hard kill without leaving
// half-written YAML on disk.
func AtomicWriteFile(fsys FS, name string, data []byte, perm os.FileMode) error {
	osfs, ok := fsys.(*OSFS)
	if !ok {
		return fsys.WriteFile(name, data, perm)
	}
	return renameio.WriteFile(osfs.resolve(name), data, perm)
}
