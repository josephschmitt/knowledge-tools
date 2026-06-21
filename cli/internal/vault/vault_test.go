package vault

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockContention(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "vault.lock")

	l1, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	// Second acquire of the same path must report ErrLocked, not block or succeed.
	if _, err := AcquireLock(lockPath); err != ErrLocked {
		t.Fatalf("second AcquireLock err = %v, want ErrLocked", err)
	}

	// After release, it can be re-acquired.
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l2, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("re-AcquireLock after release: %v", err)
	}
	_ = l2.Release()
}

func TestEpochISO(t *testing.T) {
	if got := EpochISO(0); got != "" {
		t.Errorf("EpochISO(0) = %q, want empty", got)
	}
	if got := EpochISO(-5); got != "" {
		t.Errorf("EpochISO(-5) = %q, want empty", got)
	}
	if got := EpochISO(1_700_000_000); got == "" {
		t.Error("EpochISO(positive) should be non-empty")
	}
}

// initRepo creates a git repo in a fresh temp dir with one commit, returning its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func newTestLogger(t *testing.T) *Logger {
	t.Helper()
	l, err := NewLogger(filepath.Join(t.TempDir(), "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestCommitAndPushNotARepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	if err := CommitAndPush(dir, "msg", nil, newTestLogger(t)); err != nil {
		t.Errorf("non-repo should be a clean no-op, got %v", err)
	}
}

func TestCommitAndPushNoChanges(t *testing.T) {
	repo := initRepo(t)
	if err := CommitAndPush(repo, "msg", nil, newTestLogger(t)); err != nil {
		t.Errorf("no changes should be a clean no-op, got %v", err)
	}
}

func TestCommitAndPushCommitsLocally(t *testing.T) {
	repo := initRepo(t)
	if err := writeFile(repo, "note.md", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := CommitAndPush(repo, "add note", nil, newTestLogger(t)); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	// HEAD subject should be our message (no origin → commit kept local, no error).
	out := gitOut(repo, "log", "-1", "--format=%s")
	if out != "add note" {
		t.Errorf("HEAD subject = %q, want %q", out, "add note")
	}
}

func TestCommitAndPushPathspecScoping(t *testing.T) {
	repo := initRepo(t)
	if err := writeFile(repo, "wiki.md", "keep"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(repo, "inbox.md", "skip"); err != nil {
		t.Fatal(err)
	}
	// Only stage wiki.md.
	if err := CommitAndPush(repo, "scoped", []string{"wiki.md"}, newTestLogger(t)); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}
	// inbox.md must remain uncommitted (untracked).
	if status := gitOut(repo, "status", "--porcelain"); status == "" {
		t.Error("expected inbox.md to remain untracked, but tree is clean")
	}
	committed := gitOut(repo, "show", "--name-only", "--format=", "HEAD")
	if committed != "wiki.md" {
		t.Errorf("committed files = %q, want only wiki.md", committed)
	}
}

func TestSyncFromOriginNoOrigin(t *testing.T) {
	repo := initRepo(t)
	if err := SyncFromOrigin(repo, newTestLogger(t)); err != nil {
		t.Errorf("no origin should be a no-op, got %v", err)
	}
}

func writeFile(repo, name, content string) error {
	return os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644)
}
