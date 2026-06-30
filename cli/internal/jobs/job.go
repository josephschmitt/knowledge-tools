package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/josephschmitt/knowledge-tools/cli/internal/agent"
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

	driver, err := agent.NewDriver(agent.Spec{Agent: cfg.Agent, Bin: cfg.AgentBin, Cmd: cfg.AgentCmd})
	if err != nil {
		return err
	}
	channel, channelDowngraded, err := resolveAgentChannel(channel, cfg.ReviewChannel, driver)
	if err != nil {
		return err
	}

	skill, ghTools, commitPaths := channelConfig(job, channel)

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

		if channelDowngraded {
			log.Logf("agent %q can't scope shell grants — using the files channel instead of github.", driver.Name())
		}
		log.Logf("%s starting (skill=%s, channel=%s, agent=%s)", job, skill, channel, driver.Name())

		// resolve acts ONLY on calls I've marked answered. Nothing answered → skip the run entirely,
		// the same short-circuit compile does on an empty inbox.
		if job == JobResolve {
			n := answeredCount(cfg, channel, log)
			if n == 0 {
				log.Logf("nothing answered — nothing to resolve.")
				return nil
			}
			log.Logf("answered calls: %d", n)
		}

		prompt, err := skillPrompt(repo, skill, "")
		if err != nil {
			return err
		}
		inv := agent.Invocation{
			Repo:        repo,
			Prompt:      prompt,
			Model:       cfg.JobModel(string(job)),
			Effort:      cfg.JobEffort(string(job)),
			ShellGrants: ghTools,
		}
		if err := agent.Run(ctx, driver, inv, log.File()); err != nil {
			log.Logf("agent exited non-zero — leaving the vault as-is for inspection.")
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
		} else if err := vault.CommitAndPush(repo, fmt.Sprintf("Vault %s (%s)", job, st), stage, siteRebuild(cfg), log); err != nil {
			log.Logf("%s done (with push failure).", job)
			return err
		}

		log.Logf("%s done.", job)
		return nil
	})
}

// siteRebuild builds the CommitAndPush rebuild hook from config — nil (no trigger) when
// KNOWLEDGE_SITE_REBUILD_URL is unset, so non-site deployments POST nowhere.
func siteRebuild(cfg *config.Config) *vault.SiteRebuild {
	if cfg.SiteRebuildURL == "" {
		return nil
	}
	return &vault.SiteRebuild{URL: cfg.SiteRebuildURL, Token: cfg.SiteRebuildToken}
}

// resolveAgentChannel picks the effective review channel for a driver. The github channel needs the
// agent to scope unattended shell to a few gh subcommands; an agent that can't (codex's
// all-or-nothing sandbox; a custom template without {{grants}}) must not be handed unrestricted
// shell, so an auto-detected github downgrades to the grant-free files channel. If github was forced
// explicitly (KNOWLEDGE_REVIEW_CHANNEL=github), that's an error rather than a silent downgrade.
// Returns the channel, whether it was downgraded, and any error.
func resolveAgentChannel(detected, forced string, driver agent.Driver) (string, bool, error) {
	if detected == "github" && !driver.SupportsShellGrants() {
		if forced == "github" {
			return "", false, fmt.Errorf("KNOWLEDGE_REVIEW_CHANNEL=github needs an agent that can scope shell grants, but %q cannot — use the files channel or the claude/opencode agent", driver.Name())
		}
		return "files", true, nil
	}
	return detected, false, nil
}

// channelConfig returns the skill name, neutral shell-grant prefixes, and commit pathspecs for a
// job+channel. Ports the case blocks in vault-job.sh. The grants are bare command prefixes (e.g.
// "gh issue list"); each agent driver re-encodes them into its own allowlist syntax (the claude
// driver into Bash(gh issue list:*)). They must cover exactly the gh subcommands the skill uses so
// the headless run can't stall on an unanswerable permission prompt.
func channelConfig(job Job, channel string) (skill string, ghTools, commitPaths []string) {
	// notebook/ is committed alongside library/: synthesize repairs notebook↔library origin links
	// (and notebook/index.md), so its edits must be staged. resolve is library-scoped, but listing
	// notebook/ is harmless there (git add only commits what actually changed).
	if channel == "files" {
		// The files channel writes question files under inbox/.review/, so commit that subdir too
		// — never the raw top-level inbox/ captures compile hasn't processed yet.
		return string(job) + "-files", nil, []string{"library", "notebook", "index.md", "log.md", "inbox/.review"}
	}
	// github channel.
	commitPaths = []string{"library", "notebook", "index.md", "log.md"}
	if job == JobSynthesize {
		ghTools = []string{"gh issue list", "gh issue view", "gh issue create", "gh search issues"}
	} else {
		ghTools = []string{"gh issue list", "gh issue view", "gh issue comment", "gh issue edit", "gh issue close"}
	}
	return string(job), ghTools, commitPaths
}

// skillPrompt reads <repo>/.agents/skills/<name>/SKILL.md, strips its YAML frontmatter, substitutes
// $ARGUMENTS, and returns the body the agent runs as its prompt. Feeding the body directly (rather
// than relying on the harness to resolve a slash command or auto-activate a skill) keeps the
// scheduled run deterministic across harnesses.
func skillPrompt(repo, name, args string) (string, error) {
	path := filepath.Join(repo, ".agents", "skills", name, "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill %q: %w", name, err)
	}
	body := strings.ReplaceAll(stripFrontmatter(string(b)), "$ARGUMENTS", args)
	return strings.TrimSpace(body), nil
}

// stripFrontmatter removes a leading YAML frontmatter block (--- … ---) from s, returning the body.
// If there's no opening delimiter or no closing one, s is returned unchanged.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return s
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
