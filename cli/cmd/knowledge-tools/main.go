// Command knowledge-tools is the host-side CLI for the personal LLM-wiki vault: it runs the
// vault-mutating jobs (compile / synthesize / resolve), supervises them on a schedule via a
// self-managed daemon, and installs/uninstalls that daemon as an OS autostart unit. It replaces
// the scripts/ bash tooling (vault-lib.sh, vault-compile.sh, vault-job.sh, install.sh,
// uninstall.sh, init-vault.sh).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/daemon"
	"github.com/josephschmitt/knowledge-tools/cli/internal/initvault"
	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
	"github.com/josephschmitt/knowledge-tools/cli/internal/service"
	"github.com/josephschmitt/knowledge-tools/cli/internal/site"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// Globals are the flags shared by every command. Both bind to env so a per-instance env file (or
// .env) configures them; the flag overrides the env.
type Globals struct {
	Instance string `help:"Vault instance name (multi-vault)." env:"KNOWLEDGE_INSTANCE" placeholder:"NAME"`
	Repo     string `help:"Path to the vault repo." env:"KNOWLEDGE_REPO" type:"path" placeholder:"PATH"`
}

func (g *Globals) load() (*config.Config, error) {
	return config.Load(g.Instance, g.Repo)
}

// CLI is the kong command tree.
type CLI struct {
	Globals

	Install    InstallCmd       `cmd:"" help:"Install the vault daemon as an OS autostart unit (systemd/launchd)."`
	Uninstall  UninstallCmd     `cmd:"" help:"Remove the vault daemon autostart unit (idempotent)."`
	Daemon     DaemonCmd        `cmd:"" help:"Run the long-running vault daemon (scheduler + compile watcher)."`
	Compile    CompileCmd       `cmd:"" help:"Run a one-shot inbox→library compile."`
	Synthesize SynthesizeCmd    `cmd:"" help:"Run a one-shot whole-corpus synthesize (opens judgment calls)."`
	Resolve    ResolveCmd       `cmd:"" help:"Run a one-shot resolve (applies answered judgment calls)."`
	Init       InitCmd          `cmd:"" help:"Scaffold a fresh vault from the template (copy-if-absent)."`
	Site       SiteCmd          `cmd:"" help:"Build + publish the static Quartz site the service serves."`
	Status     StatusCmd        `cmd:"" help:"Print the vault's compile + schedule status."`
	Version    kong.VersionFlag `help:"Print version and exit."`
}

func main() {
	// Load .env (real env wins) so kong's env-bound flags and config.Load see it, matching
	// scripts/load-env.sh.
	if err := config.LoadDotenv(""); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge-tools: warning: failed to read .env:", err)
	}

	cli := &CLI{}
	ctx := kong.Parse(cli,
		kong.Name("knowledge-tools"),
		kong.Description("Host-side CLI for the personal LLM-wiki vault."),
		kong.UsageOnError(),
		kong.Vars{"version": version},
	)
	err := ctx.Run(&cli.Globals)
	ctx.FatalIfErrorf(err)
}

// signalContext returns a context cancelled on SIGINT/SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// --- commands ---

type InstallCmd struct {
	CompileSchedule    string `help:"Cron schedule for compile." placeholder:"CRON"`
	SynthesizeSchedule string `help:"Cron schedule for synthesize." placeholder:"CRON"`
	ResolveSchedule    string `help:"Cron schedule for resolve." placeholder:"CRON"`
	ReviewChannel      string `help:"Judgment-call channel: github | files (default: auto-detect)." enum:"github,files," default:""`
	GithubRepo         string `help:"GitHub repo (owner/name) for the github review channel." placeholder:"OWNER/NAME"`
	Cooldown           int    `help:"Seconds between allowed manual compiles." default:"0"`
	Site               bool   `help:"Rebuild the Quartz site after each compile (needs Node>=20 on the host)."`
}

func (c *InstallCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	// Explicit flags override the env/.env/default values config.Load resolved.
	if c.CompileSchedule != "" {
		cfg.CompileSchedule = c.CompileSchedule
	}
	if c.SynthesizeSchedule != "" {
		cfg.SynthesizeSchedule = c.SynthesizeSchedule
	}
	if c.ResolveSchedule != "" {
		cfg.ResolveSchedule = c.ResolveSchedule
	}
	if c.ReviewChannel != "" {
		cfg.ReviewChannel = c.ReviewChannel
	}
	if c.GithubRepo != "" {
		cfg.GithubRepo = c.GithubRepo
	}
	if c.Cooldown > 0 {
		cfg.CompileCooldown = c.Cooldown
	}
	if c.Site {
		cfg.SiteEnable = true
	}
	return service.Install(service.Options{Cfg: cfg})
}

type UninstallCmd struct{}

func (c *UninstallCmd) Run(g *Globals) error {
	// Uninstall needs no KNOWLEDGE_REPO; load tolerates an empty repo.
	cfg, err := g.load()
	if err != nil {
		return err
	}
	return service.Uninstall(cfg)
}

type DaemonCmd struct{}

func (c *DaemonCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return daemon.Run(ctx, cfg)
}

type CompileCmd struct {
	Manual bool `help:"Treat as an on-demand compile (cooldown-throttled)."`
}

func (c *CompileCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.Compile(ctx, cfg, c.Manual))
}

type SynthesizeCmd struct{}

func (c *SynthesizeCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.RunIssueJob(ctx, cfg, jobs.JobSynthesize))
}

type ResolveCmd struct{}

func (c *ResolveCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.RunIssueJob(ctx, cfg, jobs.JobResolve))
}

type InitCmd struct {
	Dir string `arg:"" optional:"" help:"Target vault dir (default: --repo / KNOWLEDGE_REPO)." type:"path"`
}

func (c *InitCmd) Run(g *Globals) error {
	target := c.Dir
	if target == "" {
		target = g.Repo
	}
	if target == "" {
		target = os.Getenv("KNOWLEDGE_REPO")
	}
	if target == "" {
		return fmt.Errorf("no target — pass a dir or set --repo / KNOWLEDGE_REPO")
	}
	fmt.Printf("Seeding vault at: %s\n", target)
	res, err := initvault.Seed(target)
	if err != nil {
		return err
	}
	fmt.Printf("Done: %d created, %d left untouched.\n", res.Created, res.Skipped)
	if res.Created > 0 {
		fmt.Println("Next: cd into the vault, git init (if needed), and make the first commit yourself —")
		fmt.Println("init leaves git alone.")
	}
	return nil
}

type SiteCmd struct {
	NoLock bool `help:"Skip the shared lock (the caller already holds it)."`
	Soft   bool `help:"Exit success even if the build fails (leave the published site in place)."`
}

func (c *SiteCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(site.Build(ctx, cfg, site.Options{NoLock: c.NoLock, Soft: c.Soft}))
}

type StatusCmd struct{}

func (c *StatusCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	if err := cfg.RequireRepo(); err != nil {
		return err
	}
	return printStatus(cfg)
}

// ignoreLocked turns ErrLocked into a clean exit — another vault job holds the lock, which the bash
// scripts treated as a non-error.
func ignoreLocked(err error) error {
	if err == vaultErrLocked {
		return nil
	}
	return err
}
