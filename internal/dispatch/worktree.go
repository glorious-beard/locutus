package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree represents a git worktree used for isolated workstream development.
type Worktree struct {
	RepoDir     string
	WorktreeDir string
	BranchName  string
}

// gitCmd runs a git command in the given directory and returns an error with
// combined output on failure. ctx cancellation kills the underlying git
// process via exec.CommandContext.
func gitCmd(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %s: %w", args, out, err)
	}
	return nil
}

// gitOutput runs a git command in the given directory and returns its combined output.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %s: %w", args, out, err)
	}
	return string(out), nil
}

// CreateWorktree creates a new git worktree for the given workstream ID,
// based on the current HEAD (typically main). The worktree is checked
// out on a scratch branch "locutus-wt/<workstreamID>", distinct from the
// feature branch "locutus/<workstreamID>" that MergeToFeatureBranch will
// ultimately land the work on. Using the same name for both would leave
// the feature branch un-checkout-able in the main repo (git refuses to
// check out a branch already used by a worktree).
func CreateWorktree(ctx context.Context, repoDir string, workstreamID string) (*Worktree, error) {
	return createWorktreeOn(ctx, repoDir, workstreamID, "")
}

// CreateWorktreeFromBase is the resume-aware variant: the new scratch
// branch is rooted at baseBranch instead of HEAD. Used by DJ-074 resume
// so the prior run's already-merged steps (which live on
// "locutus/<workstreamID>") form the starting state. baseBranch must
// exist; an empty value falls back to HEAD-rooted (same as
// CreateWorktree).
func CreateWorktreeFromBase(ctx context.Context, repoDir, workstreamID, baseBranch string) (*Worktree, error) {
	return createWorktreeOn(ctx, repoDir, workstreamID, baseBranch)
}

func createWorktreeOn(ctx context.Context, repoDir, workstreamID, baseBranch string) (*Worktree, error) {
	branchName := "locutus-wt/" + workstreamID
	worktreeDir := filepath.Join(os.TempDir(), "locutus-wt-"+workstreamID)

	// Best-effort recovery from a prior crashed run. SIGKILL skips
	// defer wt.Cleanup(), leaving a worktree dir + git registration +
	// scratch branch behind that would otherwise make `git worktree add`
	// fail with "fatal: a branch named ... already exists" and
	// structurally block DJ-074 resume.
	recoverStaleWorktree(ctx, repoDir, workstreamID, branchName, worktreeDir)

	args := []string{"worktree", "add", "-b", branchName, worktreeDir}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}
	if err := gitCmd(ctx, repoDir, args...); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	return &Worktree{
		RepoDir:     repoDir,
		WorktreeDir: worktreeDir,
		BranchName:  branchName,
	}, nil
}

// recoverStaleWorktree removes any leftover worktree state for this
// workstream ID so `git worktree add` can proceed cleanly. Safe to call
// on a clean tree: every step is best-effort and logged-but-not-failed.
// Three artifacts can survive a SIGKILL'd run:
//
//   - the worktree directory under os.TempDir()
//   - git's registration under <repo>/.git/worktrees/<name>/
//   - the scratch branch locutus-wt/<workstreamID>
//
// Any uncommitted or committed-but-unmerged work in the orphan is
// deliberately discarded — today nothing lands on the feature branch
// until the entire workstream completes (one final wt.Commit), so there
// is nothing here for users to recover by hand. When per-step
// persistence lands (the DJ-073 follow-up), re-evaluate.
func recoverStaleWorktree(ctx context.Context, repoDir, workstreamID, branchName, worktreeDir string) {
	var actions []string

	// Prune registrations whose directories were already removed (e.g.
	// a /tmp wipe between runs). Errors are non-actionable here.
	_ = gitCmd(ctx, repoDir, "worktree", "prune")

	if _, err := os.Stat(worktreeDir); err == nil {
		// git's clean removal first — also unregisters.
		if err := gitCmd(ctx, repoDir, "worktree", "remove", worktreeDir, "--force"); err == nil {
			actions = append(actions, "removed orphan worktree dir via git")
		} else if rmErr := os.RemoveAll(worktreeDir); rmErr == nil {
			// Fallback: registration may already be gone.
			_ = gitCmd(ctx, repoDir, "worktree", "prune")
			actions = append(actions, "removed orphan worktree dir via filesystem")
		}
	}

	if branchOut, _ := gitOutput(ctx, repoDir, "branch", "--list", branchName); strings.TrimSpace(branchOut) != "" {
		if err := gitCmd(ctx, repoDir, "branch", "-D", branchName); err == nil {
			actions = append(actions, "deleted orphan scratch branch")
		}
	}

	if len(actions) > 0 {
		slog.Warn("recovered stale worktree state from prior crashed run",
			"workstream_id", workstreamID,
			"actions", actions,
		)
	}
}

// Commit stages all changes in the worktree and commits with the given message.
func (w *Worktree) Commit(ctx context.Context, message string) error {
	if err := gitCmd(ctx, w.WorktreeDir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}
	if err := gitCmd(ctx, w.WorktreeDir, "commit", "-m", message); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MergeToFeatureBranch merges the worktree branch into the given feature branch
// in the main repository. If the feature branch does not exist, it is created.
func (w *Worktree) MergeToFeatureBranch(ctx context.Context, featureBranch string) error {
	// Save the current branch so we can return to it.
	origBranch, err := gitOutput(ctx, w.RepoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}
	origBranch = strings.TrimSpace(origBranch)

	// Check if feature branch exists.
	branchOut, _ := gitOutput(ctx, w.RepoDir, "branch", "--list", featureBranch)
	if strings.TrimSpace(branchOut) == "" {
		// Create the feature branch from HEAD.
		if err := gitCmd(ctx, w.RepoDir, "branch", featureBranch); err != nil {
			return fmt.Errorf("create feature branch: %w", err)
		}
	}

	// Checkout the feature branch.
	if err := gitCmd(ctx, w.RepoDir, "checkout", featureBranch); err != nil {
		return fmt.Errorf("checkout feature branch: %w", err)
	}

	// Merge the worktree branch into the feature branch.
	mergeErr := gitCmd(ctx, w.RepoDir, "merge", w.BranchName, "--no-ff", "-m", "merge workstream")

	// Always return to the original branch, even if merge failed.
	if checkoutErr := gitCmd(ctx, w.RepoDir, "checkout", origBranch); checkoutErr != nil {
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
// Uses context.Background internally because Cleanup typically runs in a
// defer where the parent ctx may already be cancelled — we still need git
// to finish unregistering the worktree so the directory and branch don't
// orphan and block the next adopt run (DJ-074 resume hazard).
func (w *Worktree) Cleanup() error {
	ctx := context.Background()
	if err := gitCmd(ctx, w.RepoDir, "worktree", "remove", w.WorktreeDir, "--force"); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	if err := gitCmd(ctx, w.RepoDir, "branch", "-D", w.BranchName); err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}
	return nil
}
