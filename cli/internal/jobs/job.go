package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// RunIssueJob ports scripts/vault-job.sh for the two judgment-call jobs:
//
//	synthesize — heavy, infrequent whole-corpus pass. Reconciles drift + finds connections in
//	             library/ and OPENS judgment calls. Producer only.
//	resolve    — light, more frequent pass. Reads answered calls, applies them to library/ and
//	             CLOSES them. Consumer only; often a no-op (short-circuits when nothing answered).
//
// The channel (github | files) is auto-detected when KNOWLEDGE_REVIEW_CHANNEL is unset. github
// runs /<job> and grants exactly the gh issue subcommands the command needs via --allowedTools;
// files runs /<job>-files with no tool grants (file edits only). The wrapper owns git: it commits
// the scoped pathspec (never the raw top-level inbox/ captures compile hasn't processed) and
// pushes. Returns ErrLocked (cleanly) if another vault job holds the lock.
func RunIssueJob(ctx context.Context, cfg *config.Config, job Job) error {
	if job != JobSynthesize && job != JobResolve {
		return fmt.Errorf("unknown job %q (expected synthesize or resolve)", job)
	}
	if err := cfg.RequireRepo(); err != nil {
		return err
	}
	repo := cfg.Repo
	st := stamp()

	channel := detectChannel(cfg)
	if channel != "github" && channel != "files" {
		return fmt.Errorf("unknown KNOWLEDGE_REVIEW_CHANNEL %q (expected github or files)", channel)
	}

	slash, ghTools, commitPaths := channelConfig(job, channel)

	logPath := filepath.Join(repo, "outputs", string(job)+"-logs", st+".log")
	log, err := vault.NewLogger(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	// Serialize against compile + the other issue job, under the shared lock (held → clean no-op).
	// withVaultLock records the run and refreshes the schedule snapshot on exit.
	return withVaultLock(cfg, job, log, func() error {
		if err := vault.SyncFromOrigin(repo, log); err != nil {
			return err
		}

		log.Logf("%s starting (%s, channel=%s)", job, slash, channel)

		// resolve acts ONLY on calls I've marked answered. Nothing answered → skip the (opus) run
		// entirely, the same short-circuit compile does on an empty inbox.
		if job == JobResolve {
			n := answeredCount(cfg, channel, log)
			if n == 0 {
				log.Logf("nothing answered — nothing to resolve.")
				return nil
			}
			log.Logf("answered calls: %d", n)
		}

		if err := vault.RunClaude(ctx, cfg.ClaudeBin, repo, slash, ghTools, log); err != nil {
			log.Logf("claude exited non-zero — leaving the vault as-is for inspection.")
			return err
		}

		// Commit the scoped pathspec — only paths that actually exist (git add errors on a missing
		// pathspec, and inbox/.review or a freshly-seeded log.md may not be there yet).
		var stage []string
		for _, p := range commitPaths {
			if _, statErr := os.Stat(filepath.Join(repo, p)); statErr == nil {
				stage = append(stage, p)
			}
		}
		if len(stage) == 0 {
			log.Logf("no tracked paths present to commit.")
		} else if err := vault.CommitAndPush(repo, fmt.Sprintf("Vault %s (%s)", job, st), stage, log); err != nil {
			log.Logf("%s done (with push failure).", job)
			return err
		}

		log.Logf("%s done.", job)
		return nil
	})
}

// channelConfig returns the slash command, gh tool grants, and commit pathspecs for a job+channel.
// Ports the case blocks in vault-job.sh. GH_TOOLS must match the command's frontmatter
// allowed-tools exactly so the headless run can't stall on an unanswerable permission prompt.
func channelConfig(job Job, channel string) (slash string, ghTools, commitPaths []string) {
	if channel == "files" {
		// The files channel writes question files under inbox/.review/, so commit that subdir too
		// — never the raw top-level inbox/ captures compile hasn't processed yet.
		return "/" + string(job) + "-files", nil, []string{"library", "index.md", "log.md", "inbox/.review"}
	}
	// github channel.
	commitPaths = []string{"library", "index.md", "log.md"}
	if job == JobSynthesize {
		ghTools = []string{
			"Bash(gh issue list:*)",
			"Bash(gh issue view:*)",
			"Bash(gh issue create:*)",
			"Bash(gh search issues:*)",
		}
	} else {
		ghTools = []string{
			"Bash(gh issue list:*)",
			"Bash(gh issue view:*)",
			"Bash(gh issue comment:*)",
			"Bash(gh issue edit:*)",
			"Bash(gh issue close:*)",
		}
	}
	return "/" + string(job), ghTools, commitPaths
}

// answeredCount returns how many judgment calls are marked answered — the resolve short-circuit.
// github: open issues labeled vault:answered. files: inbox/.review/*.md with `status: answered`.
func answeredCount(cfg *config.Config, channel string, log *vault.Logger) int {
	if channel == "github" {
		cmd := exec.Command("gh", "issue", "list", "--state", "open", "--label", "vault:answered", "--json", "number", "-q", "length")
		cmd.Dir = cfg.Repo
		out, err := cmd.Output()
		if err != nil {
			if log != nil {
				log.Logf("WARNING: gh issue list failed — assuming nothing answered.")
			}
			return 0
		}
		var n int
		_, _ = fmt.Sscanf(string(out), "%d", &n)
		return n
	}
	return countAnsweredFiles(filepath.Join(cfg.Repo, "inbox", ".review"))
}

// countAnsweredFiles counts files under dir whose body has a `status: answered` line (the files
// channel's go-signal). Mirrors the bash `grep -rl '^status: answered'`.
func countAnsweredFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if hasAnsweredLine(b) {
			count++
		}
		return nil
	})
	return count
}

// hasAnsweredLine reports whether any line equals "status: answered" (anchored, like grep '^...').
func hasAnsweredLine(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		if line == "status: answered" {
			return true
		}
	}
	return false
}
