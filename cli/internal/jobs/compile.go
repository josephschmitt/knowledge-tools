package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// compileStatus is inbox/.compile/status.json, read by service/src/vault.ts. Missing timestamps
// are the empty string (not null) — the service's nonEmpty() treats "" as absent, matching the
// bash iso_of which printed "" for a missing epoch file.
type compileStatus struct {
	Running             bool   `json:"running"`
	StartedAt           string `json:"started_at"`
	LastCompiledAt      string `json:"last_compiled_at"`
	LastManualCompileAt string `json:"last_manual_compile_at"`
	CooldownSeconds     int    `json:"cooldown_seconds"`
	Summary             string `json:"summary"`
}

// Compile ports scripts/vault-compile.sh: a fresh, headless /compile-inbox pass that turns inbox/
// captures into library/ knowledge, archives the processed captures, and commits.
//
// manual marks an on-demand compile (the daemon's fsnotify trigger when the MCP drops
// inbox/.compile/request) — those are cooldown-throttled and consume the manual cooldown. A
// scheduled compile (cron tick) and a direct CLI run are never throttled. This replaces the bash
// script's OS-divergent request-file dance: the trigger source decides manual vs scheduled, not
// the file's presence/mtime.
//
// Returns ErrLocked (cleanly) if another vault job holds the lock — the caller should treat that
// as a no-op, like the bash exit 0.
func Compile(ctx context.Context, cfg *config.Config, manual bool) error {
	if err := cfg.RequireRepo(); err != nil {
		return err
	}
	repo := cfg.Repo
	st := stamp()

	logPath := filepath.Join(repo, "outputs", "compile-logs", st+".log")
	log, err := vault.NewLogger(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	compileDir := cfg.CompileDir()
	if err := os.MkdirAll(compileDir, 0o755); err != nil {
		return err
	}
	lastCompiledFile := filepath.Join(compileDir, "last-compiled-epoch")
	lastManualFile := filepath.Join(compileDir, "last-manual-epoch")
	statusFile := filepath.Join(compileDir, "status.json")

	startedAt := vault.NowISO()
	writeStatus := func(running bool, summary string) {
		s := compileStatus{
			Running:             running,
			StartedAt:           startedAt,
			LastCompiledAt:      vault.EpochISO(readEpoch(lastCompiledFile)),
			LastManualCompileAt: vault.EpochISO(readEpoch(lastManualFile)),
			CooldownSeconds:     cfg.CompileCooldown,
			Summary:             summary,
		}
		_ = writeJSONAtomic(statusFile, s)
	}

	// Run under the shared lock (held by another job → clean no-op). withVaultLock records the run
	// and refreshes the schedule snapshot on exit.
	return withVaultLock(cfg, JobCompile, log, func() error {
		// Catch up to origin before compiling + committing, so the push fast-forwards.
		if err := vault.SyncFromOrigin(repo, log); err != nil {
			writeStatus(false, "aborted: local diverged from origin")
			return err
		}

		mode := "scheduled"
		if manual {
			mode = "manual"
		}
		log.Logf("compile mode: %s", mode)

		// Manual runs are throttled; the scheduled run is exempt and never consumes the cooldown.
		if manual {
			if last := readEpoch(lastManualFile); last > 0 {
				elapsed := time.Now().Unix() - last
				if elapsed < int64(cfg.CompileCooldown) {
					log.Logf("throttled — last manual compile was %ds ago (< %ds). Skipping.", elapsed, cfg.CompileCooldown)
					return nil
				}
			}
		}

		// Snapshot the inbox items to process: top-level files, excluding dotfiles (.gitkeep) and
		// the .compile/ control dir.
		items, err := snapshotInbox(repo)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			log.Logf("inbox empty — nothing to compile.")
			writeStatus(false, "inbox empty")
			return nil
		}

		log.Logf("compiling %d inbox item(s):", len(items))
		for _, it := range items {
			_, _ = fmt.Fprintf(log.File(), "  %s\n", it)
		}
		writeStatus(true, fmt.Sprintf("compiling %d item(s)", len(items)))

		// Fresh, headless compile. acceptEdits auto-applies Write/Edit without prompting.
		if err := vault.RunClaude(ctx, cfg.ClaudeBin, repo, "/compile-inbox", nil, log); err != nil {
			log.Logf("claude exited non-zero — leaving inbox untouched for inspection.")
			writeStatus(false, "compile failed")
			return err
		}

		// Archive the captures we processed (preserve a raw trail per the vault's CLAUDE.md).
		archive := filepath.Join(repo, "inbox", "archive", st)
		if err := os.MkdirAll(archive, 0o755); err != nil {
			writeStatus(false, "compile failed at archive")
			return err
		}
		for _, it := range items {
			src := filepath.Join(repo, it)
			if _, statErr := os.Stat(src); statErr == nil {
				_ = os.Rename(src, filepath.Join(archive, filepath.Base(it)))
			}
		}
		log.Logf("archived processed captures to inbox/archive/%s", st)

		// Commit if anything changed; push only if origin exists. Defer a push failure so the
		// cooldown/status bookkeeping still runs, then re-raise it so the run is flagged.
		pushFailed := false
		if err := vault.CommitAndPush(repo, fmt.Sprintf("Vault compile (%s)", st), nil, log); err != nil {
			if _, ok := err.(*vault.PushError); ok {
				pushFailed = true
			} else {
				writeStatus(false, "compile failed at commit")
				return err
			}
		}

		// Record completion timestamps for the cooldown + status surface.
		_ = writeEpochNow(lastCompiledFile)
		if manual {
			_ = writeEpochNow(lastManualFile)
		}

		if pushFailed {
			writeStatus(false, fmt.Sprintf("compiled %d item(s) but push failed", len(items)))
			log.Logf("done (with push failure).")
			return &vault.PushError{}
		}
		writeStatus(false, fmt.Sprintf("compiled %d item(s)", len(items)))
		log.Logf("done.")
		return nil
	})
}

// snapshotInbox returns the top-level inbox capture files (relative to repo), excluding dotfiles
// and the .compile/ control dir, sorted — matching the bash `find inbox -maxdepth 1 -type f !
// -name '.*'`.
func snapshotInbox(repo string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repo, "inbox"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []string
	for _, e := range entries {
		if e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		items = append(items, filepath.Join("inbox", e.Name()))
	}
	sort.Strings(items)
	return items, nil
}
