package autotune

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// skipIfNoGit skips the test if git is not available.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping")
	}
}

// createBareRepo creates a bare git repo for testing push operations.
// Returns the bare repo path and the default branch name.
func createBareRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")

	// Init bare repo with explicit initial branch name.
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback for older git versions without --initial-branch.
		cmd = exec.Command("git", "init", "--bare", bare)
		if out2, err2 := cmd.CombinedOutput(); err2 != nil {
			t.Fatalf("git init --bare failed: %v\n%s\n%s", err2, out, out2)
		}
	}

	// Create an initial commit in a working clone so the branch exists.
	work := filepath.Join(dir, "work")
	cmd = exec.Command("git", "clone", bare, work)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, out)
	}

	// Configure user in working clone and disable signing.
	exec.Command("git", "-C", work, "config", "user.name", "test").Run()
	exec.Command("git", "-C", work, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", work, "config", "commit.gpgsign", "false").Run()

	// Create initial commit on "main" branch.
	exec.Command("git", "-C", work, "checkout", "-b", "main").Run()
	readmePath := filepath.Join(work, "README.md")
	os.WriteFile(readmePath, []byte("init"), 0o644)
	exec.Command("git", "-C", work, "add", "-A").Run()
	exec.Command("git", "-C", work, "commit", "-m", "initial").Run()
	exec.Command("git", "-C", work, "push", "-u", "origin", "main").Run()

	return bare, "main"
}

func TestGitPersister_CommitMessage(t *testing.T) {
	gp := &GitPersister{
		cfg: GitPersistConfig{CommitPrefix: "autotune: "},
	}

	// Single change.
	msg := gp.buildCommitMessage([]ConfigChange{
		{Action: "increase", Domain: "limits", RuleName: "rule_a", OldValue: 1000, NewValue: 1250, Reason: "high util"},
	})
	if msg != "autotune: increase limits rule_a: 1000 → 1250 (high util)" {
		t.Errorf("unexpected commit message: %s", msg)
	}

	// Multiple changes.
	msg = gp.buildCommitMessage([]ConfigChange{
		{Action: "increase", RuleName: "a"},
		{Action: "decrease", RuleName: "b"},
	})
	if msg != "autotune: 2 adjustments in cycle" {
		t.Errorf("unexpected commit message: %s", msg)
	}

	// No changes.
	msg = gp.buildCommitMessage(nil)
	if msg != "autotune: no-op cycle" {
		t.Errorf("unexpected commit message: %s", msg)
	}
}

func TestGitPersister_AuthErrorDetection(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"Authentication failed for https://github.com/...", true},
		{"Permission denied (publickey)", true},
		{"could not read Username for 'https://github.com'", true},
		{"connection refused", false},
		{"network timeout", false},
	}

	for _, tt := range tests {
		got := isAuthError(errors.New(tt.errMsg))
		if got != tt.want {
			t.Errorf("isAuthError(%q) = %v, want %v", tt.errMsg, got, tt.want)
		}
	}
}

func TestGitPersister_NonFastForwardDetection(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"! [rejected] main -> main (non-fast-forward)", true},
		{"Updates were rejected because the tip is behind. fetch first", true},
		{"connection refused", false},
	}

	for _, tt := range tests {
		got := isNonFastForward(errors.New(tt.errMsg))
		if got != tt.want {
			t.Errorf("isNonFastForward(%q) = %v, want %v", tt.errMsg, got, tt.want)
		}
	}
}

func TestGitPersister_FullCycle(t *testing.T) {
	skipIfNoGit(t)

	bare, branch := createBareRepo(t)

	gp := NewGitPersister(
		GitPersistConfig{
			RepoURL:      "file://" + bare,
			Branch:       branch,
			Path:         "autotune",
			CommitPrefix: "test: ",
			AuthorName:   "test-author",
			AuthorEmail:  "test@example.com",
		},
		GitSafeguardConfig{
			PushTimeout:      30 * time.Second,
			MaxRetries:       3,
			RetryBackoffBase: 100 * time.Millisecond,
			LockStaleTimeout: 5 * time.Minute,
		},
	)

	ctx := context.Background()
	changes := []ConfigChange{
		{
			Timestamp: time.Now(),
			Domain:    "limits",
			RuleName:  "rule_a",
			OldValue:  1000,
			NewValue:  1250,
			Action:    "increase",
			Reason:    "high utilization",
		},
	}

	if err := gp.Persist(ctx, changes); err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	// Verify the changes file exists in the working clone.
	changePath := filepath.Join(gp.repoDir, "autotune", "changes.json")
	if _, err := os.Stat(changePath); os.IsNotExist(err) {
		t.Fatal("changes.json not created")
	}

	// Load history and verify.
	loaded, err := gp.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 change, got %d", len(loaded))
	}
	if loaded[0].RuleName != "rule_a" {
		t.Errorf("expected rule_a, got %s", loaded[0].RuleName)
	}

	// Verify commit was pushed to bare repo.
	work2 := t.TempDir()
	cmd := exec.Command("git", "clone", "file://"+bare, work2)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verification clone failed: %v\n%s", err, out)
	}

	verifyPath := filepath.Join(work2, "autotune", "changes.json")
	if _, err := os.Stat(verifyPath); os.IsNotExist(err) {
		t.Fatal("changes.json not pushed to remote")
	}
}

func TestGitPersister_StaleLockCleanup(t *testing.T) {
	skipIfNoGit(t)

	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)

	lockPath := filepath.Join(gitDir, "index.lock")
	os.WriteFile(lockPath, []byte("stale"), 0o644)

	// Set mod time to 10 minutes ago.
	os.Chtimes(lockPath, time.Now().Add(-10*time.Minute), time.Now().Add(-10*time.Minute))

	gp := &GitPersister{
		repoDir: dir,
		safeCfg: GitSafeguardConfig{LockStaleTimeout: 5 * time.Minute},
	}

	gp.cleanStaleLock()

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("stale lock file should have been removed")
	}
}

func TestGitPersister_FreshLockNotCleaned(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)

	lockPath := filepath.Join(gitDir, "index.lock")
	os.WriteFile(lockPath, []byte("active"), 0o644)

	gp := &GitPersister{
		repoDir: dir,
		safeCfg: GitSafeguardConfig{LockStaleTimeout: 5 * time.Minute},
	}

	gp.cleanStaleLock()

	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("fresh lock file should NOT have been removed")
	}
}
