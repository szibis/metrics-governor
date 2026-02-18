package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GitPersister commits autotune changes directly to a git repository.
// It clones (or pulls) the repo on first use, writes changes to a JSON file,
// and commits+pushes with a descriptive message.
type GitPersister struct {
	cfg     GitPersistConfig
	safeCfg GitSafeguardConfig

	mu      sync.Mutex
	repoDir string // local clone directory
	cloned  bool   // whether we've cloned/initialized
}

// NewGitPersister creates a git-based change persister.
func NewGitPersister(cfg GitPersistConfig, safeGuards GitSafeguardConfig) *GitPersister {
	return &GitPersister{
		cfg:     cfg,
		safeCfg: safeGuards,
	}
}

// Persist commits changes to the git repo.
func (gp *GitPersister) Persist(ctx context.Context, changes []ConfigChange) error {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	if err := gp.ensureCloned(ctx); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	// Pull latest.
	if err := gp.gitPull(ctx); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}

	// Write changes file.
	changePath := filepath.Join(gp.repoDir, gp.cfg.Path, "changes.json")
	if err := gp.writeChanges(changePath, changes); err != nil {
		return fmt.Errorf("write changes: %w", err)
	}

	// Commit.
	commitMsg := gp.buildCommitMessage(changes)
	if err := gp.gitCommit(ctx, commitMsg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	// Push with retries.
	if err := gp.gitPushWithRetries(ctx); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	return nil
}

// LoadHistory reads the change history from the git repo's changes file.
func (gp *GitPersister) LoadHistory(ctx context.Context) ([]ConfigChange, error) {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	if err := gp.ensureCloned(ctx); err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}

	changePath := filepath.Join(gp.repoDir, gp.cfg.Path, "changes.json")
	data, err := os.ReadFile(changePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read changes: %w", err)
	}

	var changes []ConfigChange
	if err := json.Unmarshal(data, &changes); err != nil {
		return nil, fmt.Errorf("unmarshal changes: %w", err)
	}
	return changes, nil
}

// ensureCloned clones the repo if not already done.
func (gp *GitPersister) ensureCloned(ctx context.Context) error {
	if gp.cloned {
		return nil
	}

	dir, err := os.MkdirTemp("", "autotune-git-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	gp.repoDir = dir

	// Shallow clone — full history not needed for config writes.
	ctx, cancel := context.WithTimeout(ctx, gp.safeCfg.PushTimeout)
	defer cancel()

	args := []string{"clone", "--depth=1", "--branch", gp.cfg.Branch, gp.cfg.RepoURL, dir}
	if err := gp.runGit(ctx, "", args...); err != nil {
		// Check for auth failure — don't retry, it won't self-fix.
		if isAuthError(err) {
			return fmt.Errorf("git authentication failed (check credentials): %w", err)
		}
		return err
	}

	// Configure author and disable signing for automated commits.
	for _, kv := range [][2]string{
		{"user.name", gp.cfg.AuthorName},
		{"user.email", gp.cfg.AuthorEmail},
		{"commit.gpgsign", "false"},
	} {
		if err := gp.runGit(ctx, dir, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}

	gp.cloned = true
	return nil
}

// gitPull pulls the latest changes.
func (gp *GitPersister) gitPull(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, gp.safeCfg.PushTimeout)
	defer cancel()

	// Clean stale lock files.
	gp.cleanStaleLock()

	return gp.runGit(ctx, gp.repoDir, "pull", "--rebase")
}

// gitCommit stages and commits all changes.
func (gp *GitPersister) gitCommit(ctx context.Context, message string) error {
	if err := gp.runGit(ctx, gp.repoDir, "add", "-A"); err != nil {
		return err
	}

	// Check if there are changes to commit.
	err := gp.runGit(ctx, gp.repoDir, "diff", "--cached", "--quiet")
	if err == nil {
		// No changes to commit.
		return nil
	}

	return gp.runGit(ctx, gp.repoDir, "commit", "-m", message)
}

// gitPushWithRetries pushes with exponential backoff.
func (gp *GitPersister) gitPushWithRetries(ctx context.Context) error {
	var lastErr error
	for attempt := 0; attempt <= gp.safeCfg.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(gp.safeCfg.RetryBackoffBase * time.Duration(1<<(attempt-1)))
		}

		pushCtx, cancel := context.WithTimeout(ctx, gp.safeCfg.PushTimeout)
		err := gp.runGit(pushCtx, gp.repoDir, "push")
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		// Auth errors won't self-fix — stop retrying.
		if isAuthError(err) {
			return fmt.Errorf("git push authentication failed: %w", err)
		}

		// Non-fast-forward — rebase and retry.
		if isNonFastForward(err) {
			if rbErr := gp.runGit(ctx, gp.repoDir, "pull", "--rebase"); rbErr != nil {
				return fmt.Errorf("git rebase after non-fast-forward failed: %w", rbErr)
			}
		}
	}
	return fmt.Errorf("git push failed after %d retries: %w", gp.safeCfg.MaxRetries, lastErr)
}

// cleanStaleLock removes stale .git/index.lock files.
func (gp *GitPersister) cleanStaleLock() {
	lockPath := filepath.Join(gp.repoDir, ".git", "index.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		return // no lock file
	}
	if time.Since(info.ModTime()) > gp.safeCfg.LockStaleTimeout {
		os.Remove(lockPath)
	}
}

// writeChanges appends changes to the JSON file.
func (gp *GitPersister) writeChanges(path string, changes []ConfigChange) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Load existing.
	var existing []ConfigChange
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing = append(existing, changes...)

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// buildCommitMessage creates a descriptive commit message.
func (gp *GitPersister) buildCommitMessage(changes []ConfigChange) string {
	if len(changes) == 0 {
		return gp.cfg.CommitPrefix + "no-op cycle"
	}
	if len(changes) == 1 {
		c := changes[0]
		return fmt.Sprintf("%s%s %s %s: %d → %d (%s)",
			gp.cfg.CommitPrefix, c.Action, c.Domain, c.RuleName, c.OldValue, c.NewValue, c.Reason)
	}
	return fmt.Sprintf("%s%d adjustments in cycle", gp.cfg.CommitPrefix, len(changes))
}

// runGit executes a git command and returns any error.
func (gp *GitPersister) runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// isAuthError checks if a git error is an authentication failure.
func isAuthError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Authentication failed") ||
		strings.Contains(s, "Permission denied") ||
		strings.Contains(s, "could not read Username")
}

// isNonFastForward checks if a git push failed due to non-fast-forward.
func isNonFastForward(err error) bool {
	s := err.Error()
	return strings.Contains(s, "non-fast-forward") ||
		strings.Contains(s, "fetch first")
}
