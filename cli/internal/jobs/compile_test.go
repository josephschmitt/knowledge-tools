package jobs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// acquireForTest holds the vault lock and returns a release func.
func acquireForTest(path string) (func(), error) {
	l, err := vault.AcquireLock(path)
	if err != nil {
		return nil, err
	}
	return func() { _ = l.Release() }, nil
}

func TestSnapshotInbox(t *testing.T) {
	repo := t.TempDir()
	mkInbox(t, repo)
	must(t, os.WriteFile(filepath.Join(repo, "inbox", "b.md"), []byte("b"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "inbox", "a.md"), []byte("a"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "inbox", ".gitkeep"), nil, 0o644))
	must(t, os.MkdirAll(filepath.Join(repo, "inbox", ".compile"), 0o755))
	must(t, os.MkdirAll(filepath.Join(repo, "inbox", "archive"), 0o755))

	items, err := snapshotInbox(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("inbox", "a.md"), filepath.Join("inbox", "b.md")}
	if len(items) != len(want) {
		t.Fatalf("items = %v, want %v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %q, want %q", i, items[i], want[i])
		}
	}
}

func TestRefreshSchedulesShape(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{
		Repo:               repo,
		Instance:           "test",
		CompileSchedule:    "@hourly",
		SynthesizeSchedule: "CRON_TZ=America/Detroit 30 4 * * 0",
		ResolveSchedule:    "30 3 * * *",
	}
	RefreshSchedules(cfg)

	data, err := os.ReadFile(filepath.Join(cfg.CompileDir(), "schedules.json"))
	if err != nil {
		t.Fatalf("schedules.json not written: %v", err)
	}
	var f struct {
		Instance string `json:"instance"`
		Jobs     map[string]struct {
			LastRunAt *string `json:"last_run_at"`
			NextRunAt *string `json:"next_run_at"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("invalid schedules.json: %v", err)
	}
	if f.Instance != "test" {
		t.Errorf("instance = %q", f.Instance)
	}
	for _, job := range []string{"compile", "synthesize", "resolve"} {
		row, ok := f.Jobs[job]
		if !ok {
			t.Errorf("missing job %q", job)
			continue
		}
		// Never run yet → last null; valid schedule → next non-null.
		if row.LastRunAt != nil {
			t.Errorf("%s last_run_at = %v, want null", job, *row.LastRunAt)
		}
		if row.NextRunAt == nil {
			t.Errorf("%s next_run_at = null, want a time", job)
		}
	}
}

func TestCompileEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a shell script")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	// Seed one capture.
	must(t, os.WriteFile(filepath.Join(repo, "inbox", "capture.md"), []byte("a thought"), 0o644))

	// Stub claude: on /compile-inbox it writes a wiki note (what the real compile would produce).
	claude := stubClaude(t, "mkdir -p wiki && echo compiled > wiki/note.md")

	cfg := &config.Config{
		Repo:               repo,
		Instance:           "test",
		ClaudeBin:          claude,
		CompileCooldown:    3600,
		VaultLock:          filepath.Join(t.TempDir(), "vault.lock"),
		CompileSchedule:    "@hourly",
		SynthesizeSchedule: "@daily",
		ResolveSchedule:    "@daily",
	}

	if err := Compile(context.Background(), cfg, false); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// The capture was archived out of the inbox.
	if _, err := os.Stat(filepath.Join(repo, "inbox", "capture.md")); !os.IsNotExist(err) {
		t.Error("capture.md should have been archived out of inbox/")
	}
	archived := false
	_ = filepath.Walk(filepath.Join(repo, "inbox", "archive"), func(p string, info os.FileInfo, _ error) error {
		if info != nil && info.Name() == "capture.md" {
			archived = true
		}
		return nil
	})
	if !archived {
		t.Error("capture.md should exist under inbox/archive/")
	}

	// The wiki note the stub produced is present.
	if _, err := os.Stat(filepath.Join(repo, "wiki", "note.md")); err != nil {
		t.Errorf("wiki/note.md missing: %v", err)
	}

	// status.json reports a finished, successful compile.
	var st struct {
		Running bool   `json:"running"`
		Summary string `json:"summary"`
	}
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "status.json"), &st)
	if st.Running {
		t.Error("status.running should be false after compile")
	}
	if st.Summary != "compiled 1 item(s)" {
		t.Errorf("status.summary = %q", st.Summary)
	}

	// A commit was made.
	if subj := gitSubject(t, repo); subj == "init" {
		t.Error("expected a compile commit, HEAD is still the init commit")
	}

	// schedules.json now shows compile last_run_at populated.
	var sched struct {
		Jobs map[string]struct {
			LastRunAt *string `json:"last_run_at"`
		} `json:"jobs"`
	}
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "schedules.json"), &sched)
	if sched.Jobs["compile"].LastRunAt == nil {
		t.Error("compile last_run_at should be set after a run")
	}
}

func TestCompileEmptyInbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a shell script")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	claude := stubClaude(t, "echo should-not-run >&2; exit 1") // must not be invoked

	cfg := &config.Config{
		Repo: repo, Instance: "test", ClaudeBin: claude, CompileCooldown: 3600,
		VaultLock:       filepath.Join(t.TempDir(), "vault.lock"),
		CompileSchedule: "@hourly", SynthesizeSchedule: "@daily", ResolveSchedule: "@daily",
	}
	if err := Compile(context.Background(), cfg, false); err != nil {
		t.Fatalf("Compile on empty inbox should succeed, got %v", err)
	}
	var st struct {
		Summary string `json:"summary"`
	}
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "status.json"), &st)
	if st.Summary != "inbox empty" {
		t.Errorf("status.summary = %q, want 'inbox empty'", st.Summary)
	}
}

func TestCompileLockHeld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock semantics differ; covered on unix")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	must(t, os.WriteFile(filepath.Join(repo, "inbox", "x.md"), []byte("x"), 0o644))
	lockPath := filepath.Join(t.TempDir(), "vault.lock")

	// Hold the lock so Compile bails as a no-op.
	held, err := acquireForTest(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer held()

	cfg := &config.Config{
		Repo: repo, Instance: "test", ClaudeBin: stubClaude(t, "exit 1"), CompileCooldown: 3600,
		VaultLock: lockPath, CompileSchedule: "@hourly", SynthesizeSchedule: "@daily", ResolveSchedule: "@daily",
	}
	err = Compile(context.Background(), cfg, false)
	if err == nil || err.Error() != "another vault job holds the lock" {
		t.Fatalf("Compile with held lock err = %v, want ErrLocked", err)
	}
	// The capture must NOT have been archived (no work done).
	if _, statErr := os.Stat(filepath.Join(repo, "inbox", "x.md")); statErr != nil {
		t.Error("inbox capture should be untouched when the lock is held")
	}
}

// --- helpers ---

func mkInbox(t *testing.T, repo string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(repo, "inbox"), 0o755))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// stubClaude writes an executable shell script that runs body (cwd = the vault) and returns its
// absolute path. It ignores claude's args.
func stubClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n" + body + "\n"
	must(t, os.WriteFile(p, []byte(script), 0o755))
	return p
}

func initGitRepo(t *testing.T) string {
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

func gitSubject(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:len(out)-1]) // strip trailing newline
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
