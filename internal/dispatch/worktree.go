package dispatch

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"os"
	"strings"
)

// Worktree represents a git worktree used for isolated workstream development.
type Worktree struct {
	RepoDir     string
	WorktreeDir string
	BranchName  string
}

// gitCmd runs a git command in the given directory and returns an error with
// combined output on failure.
func gitCmd(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s: %w", args, out, err)
	}
	return nil
}

// gitOutput runs a git command in the given directory and returns its combined output.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %s: %w", args, out, err)
	}
	return string(out), nil
}

// CreateWorktree creates a new git worktree for the given workstream ID.
// The worktree is placed in a temp directory and checked out on a new branch
// named "locutus/<workstreamID>".
func CreateWorktree(repoDir string, workstreamID string) (*Worktree, error) {
	branchName := "locutus/" + workstreamID
	worktreeDir := filepath.Join(os.TempDir(), "locutus-wt-"+workstreamID)

	err := gitCmd(repoDir, "worktree", "add", "-b", branchName, worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	return &Worktree{
		RepoDir:     repoDir,
		WorktreeDir: worktreeDir,
		BranchName:  branchName,
	}, nil
}

// Commit stages all changes in the worktree and commits with the given message.
func (w *Worktree) Commit(message string) error {
	if err := gitCmd(w.WorktreeDir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}
	if err := gitCmd(w.WorktreeDir, "commit", "-m", message); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MergeToFeatureBranch merges the worktree branch into the given feature branch
// in the main repository. If the feature branch does not exist, it is created.
func (w *Worktree) MergeToFeatureBranch(featureBranch string) error {
	// Save the current branch so we can return to it.
	origBranch, err := gitOutput(w.RepoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}
	origBranch = strings.TrimSpace(origBranch)

	// Check if feature branch exists.
	branchOut, _ := gitOutput(w.RepoDir, "branch", "--list", featureBranch)
	if strings.TrimSpace(branchOut) == "" {
		// Create the feature branch from HEAD.
		if err := gitCmd(w.RepoDir, "branch", featureBranch); err != nil {
			return fmt.Errorf("create feature branch: %w", err)
		}
	}

	// Checkout the feature branch.
	if err := gitCmd(w.RepoDir, "checkout", featureBranch); err != nil {
		return fmt.Errorf("checkout feature branch: %w", err)
	}

	// Merge the worktree branch into the feature branch.
	mergeErr := gitCmd(w.RepoDir, "merge", w.BranchName, "--no-ff", "-m", "merge workstream")

	// Always return to the original branch, even if merge failed.
	if checkoutErr := gitCmd(w.RepoDir, "checkout", origBranch); checkoutErr != nil {
		if mergeErr != nil {
			return fmt.Errorf("merge failed: %w; also failed to restore branch: %v", mergeErr, checkoutErr)
		}
		return fmt.Errorf("restore original branch: %w", checkoutErr)
	}

	if mergeErr != nil {
		return fmt.Errorf("merge: %w", mergeErr)
	}
	return nil
}

// Cleanup removes the worktree and deletes its branch from the repository.
func (w *Worktree) Cleanup() error {
	if err := gitCmd(w.RepoDir, "worktree", "remove", w.WorktreeDir, "--force"); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	if err := gitCmd(w.RepoDir, "branch", "-D", w.BranchName); err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}
	return nil
}
