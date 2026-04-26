package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// setupTestRepo creates a real temporary git repo with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644)
	assert.NoError(t, err)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
	return dir
}

// run executes a command in the given directory and fails the test on error.
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NoError(t, err, "command failed: %s %v\n%s", name, args, out)
}

// runOutput executes a command and returns its combined output.
func runOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NoError(t, err, "command failed: %s %v\n%s", name, args, out)
	return string(out)
}

func TestCreateWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)

	wt, err := CreateWorktree(context.Background(), repoDir, "ws-auth-001")
	assert.NoError(t, err)
	assert.NotNil(t, wt)

	// WorktreeDir exists on disk.
	_, statErr := os.Stat(wt.WorktreeDir)
	assert.NoError(t, statErr, "worktree directory should exist")

	// WorktreeDir is a different path from the main repo.
	assert.NotEqual(t, repoDir, wt.WorktreeDir)

	// BranchName is set and non-empty.
	assert.NotEmpty(t, wt.BranchName)

	// RepoDir is stored correctly.
	assert.Equal(t, repoDir, wt.RepoDir)

	// Cleanup so the temp dir can be removed.
	_ = wt.Cleanup()
}

func TestWorktreeCommit(t *testing.T) {
	repoDir := setupTestRepo(t)

	wt, err := CreateWorktree(context.Background(), repoDir, "ws-commit-test")
	assert.NoError(t, err)
	assert.NotNil(t, wt)
	defer func() { _ = wt.Cleanup() }()

	// Write a new file inside the worktree.
	newFile := filepath.Join(wt.WorktreeDir, "handler.go")
	err = os.WriteFile(newFile, []byte("package main\n"), 0o644)
	assert.NoError(t, err)

	// Commit the change.
	err = wt.Commit(context.Background(), "add handler stub")
	assert.NoError(t, err)

	// Verify git log in the worktree shows the commit message.
	logOut := runOutput(t, wt.WorktreeDir, "git", "log", "--oneline", "-1")
	assert.Contains(t, logOut, "add handler stub")
}

func TestMergeToFeatureBranch(t *testing.T) {
	repoDir := setupTestRepo(t)

	wt, err := CreateWorktree(context.Background(), repoDir, "ws-merge-test")
	assert.NoError(t, err)
	assert.NotNil(t, wt)
	defer func() { _ = wt.Cleanup() }()

	// Create and commit a file in the worktree.
	newFile := filepath.Join(wt.WorktreeDir, "auth.go")
	err = os.WriteFile(newFile, []byte("package auth\n"), 0o644)
	assert.NoError(t, err)
	err = wt.Commit(context.Background(), "add auth module")
	assert.NoError(t, err)

	// Merge into a feature branch.
	featureBranch := "locutus/feat-auth"
	err = wt.MergeToFeatureBranch(context.Background(), featureBranch)
	assert.NoError(t, err)

	// Verify the feature branch exists in the main repo.
	branchOut := runOutput(t, repoDir, "git", "branch", "--list", featureBranch)
	assert.Contains(t, branchOut, featureBranch, "feature branch should exist in the main repo")

	// Verify the feature branch contains the file by checking it out and listing.
	lsOut := runOutput(t, repoDir, "git", "ls-tree", "--name-only", featureBranch)
	assert.Contains(t, lsOut, "auth.go", "feature branch should contain auth.go")
}

func TestWorktreeCleanup(t *testing.T) {
	repoDir := setupTestRepo(t)

	wt, err := CreateWorktree(context.Background(), repoDir, "ws-cleanup-test")
	assert.NoError(t, err)
	assert.NotNil(t, wt)

	worktreeDir := wt.WorktreeDir
	branchName := wt.BranchName

	// Cleanup.
	err = wt.Cleanup()
	assert.NoError(t, err)

	// Verify the worktree directory is removed.
	_, statErr := os.Stat(worktreeDir)
	assert.True(t, os.IsNotExist(statErr), "worktree directory should be removed after cleanup")

	// Verify the branch is deleted from the repo.
	branchOut := runOutput(t, repoDir, "git", "branch", "--list", branchName)
	trimmed := strings.TrimSpace(branchOut)
	assert.Empty(t, trimmed, "worktree branch should be deleted after cleanup")
}

func TestMultipleWorktrees(t *testing.T) {
	repoDir := setupTestRepo(t)

	wt1, err := CreateWorktree(context.Background(), repoDir, "ws-alpha")
	assert.NoError(t, err)
	assert.NotNil(t, wt1)
	defer func() { _ = wt1.Cleanup() }()

	wt2, err := CreateWorktree(context.Background(), repoDir, "ws-beta")
	assert.NoError(t, err)
	assert.NotNil(t, wt2)
	defer func() { _ = wt2.Cleanup() }()

	// Both worktree directories exist.
	_, err1 := os.Stat(wt1.WorktreeDir)
	assert.NoError(t, err1, "first worktree should exist")
	_, err2 := os.Stat(wt2.WorktreeDir)
	assert.NoError(t, err2, "second worktree should exist")

	// Different paths.
	assert.NotEqual(t, wt1.WorktreeDir, wt2.WorktreeDir, "worktrees should have different directories")

	// Different branch names.
	assert.NotEqual(t, wt1.BranchName, wt2.BranchName, "worktrees should have different branch names")

	// Both are distinct from the main repo.
	assert.NotEqual(t, repoDir, wt1.WorktreeDir)
	assert.NotEqual(t, repoDir, wt2.WorktreeDir)
}
