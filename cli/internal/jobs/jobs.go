// Package jobs runs the three vault-mutating host jobs ported from scripts/vault-compile.sh and
// scripts/vault-job.sh: compile (inbox→wiki), synthesize (open judgment calls), and resolve
// (apply answered calls). Each acquires the shared per-instance lock, syncs from origin, runs a
// headless Claude pass, and commits via internal/vault — and refreshes the schedules.json
// snapshot on every exit path so vault_status stays current.
package jobs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// Job is one of the three scheduled host jobs.
type Job string

const (
	JobCompile    Job = "compile"
	JobSynthesize Job = "synthesize"
	JobResolve    Job = "resolve"
)

// stamp is the per-run log/archive timestamp, e.g. 2026-06-18_134500 (matches the bash `date
// +%Y-%m-%d_%H%M%S`).
func stamp() string { return time.Now().Format("2006-01-02_150405") }

// lastRunFile is where each job records its most recent run attempt (drives schedules.json's
// last_run_at and the daemon's startup catch-up). Lives in the compile coordination dir.
func lastRunFile(cfg *config.Config, job Job) string {
	return filepath.Join(cfg.CompileDir(), "last-run-"+string(job)+"-epoch")
}

// readEpoch reads an epoch-seconds file, or 0 if missing/unparseable.
func readEpoch(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// writeEpochNow writes the current epoch seconds to path (creating parent dirs).
func writeEpochNow(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
}

// withVaultLock runs fn under the shared per-instance lock with the common job preamble: it
// records the run and refreshes the schedule snapshot on exit (the bash `trap 'refresh_schedules'
// EXIT` placed after the lock). If another vault job holds the lock it returns vault.ErrLocked
// cleanly — no run recorded, no refresh — matching the bash lock-held exit that precedes the trap.
func withVaultLock(cfg *config.Config, job Job, log *vault.Logger, fn func() error) error {
	lock, err := vault.AcquireLock(cfg.VaultLock)
	if err != nil {
		if err == vault.ErrLocked {
			log.Logf("another vault job holds the lock (%s) — exiting.", cfg.VaultLock)
			return vault.ErrLocked
		}
		return err
	}
	defer func() { _ = lock.Release() }()
	recordRun(cfg, job)
	defer RefreshSchedules(cfg)
	return fn()
}

// LastRun returns when a job last ran (from its last-run epoch file), or the zero time if it has
// never run. The daemon uses this for startup catch-up.
func LastRun(cfg *config.Config, job Job) time.Time {
	if epoch := readEpoch(lastRunFile(cfg, job)); epoch > 0 {
		return time.Unix(epoch, 0)
	}
	return time.Time{}
}

// detectChannel resolves the judgment-call channel for synthesize/resolve. Ports vault-job.sh:
// honor KNOWLEDGE_REVIEW_CHANNEL when set, else prefer "github" only if gh is authed AND an origin
// remote exists, else fall back to the portable "files" queue.
func detectChannel(cfg *config.Config) string {
	if cfg.ReviewChannel != "" {
		return cfg.ReviewChannel
	}
	if ghAuthOK() && vault.HasOrigin(cfg.Repo) {
		return "github"
	}
	return "files"
}

func ghAuthOK() bool {
	return exec.Command("gh", "auth", "status").Run() == nil
}
