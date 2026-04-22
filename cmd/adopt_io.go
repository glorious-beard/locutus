package cmd

import (
	"os"
)

// statFile and readFile are thin wrappers over os for the assertion
// runner. They exist so tests that want to stub file I/O (e.g., against a
// MemFS fixture) can substitute the package-level vars, without coupling
// the assertion runner to the specio.FS interface — assertions run against
// a real working tree (a git worktree produced by the dispatcher), not
// against the spec FS abstraction.
var (
	statFile = os.Stat
	readFile = os.ReadFile
)
