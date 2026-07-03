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
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// Globals are the flags shared by every command. Both bind to env so a per-instance env file (or
// .env) configures them; the flag overrides the env.
type Globals struct {
	Instance string `help:"Vault instance name (multi-vault)." env:"KNOWLEDGE_INSTANCE" placeholder:"NAME"`
	Repo     string `name:"vault" help:"Path to the vault." env:"KNOWLEDGE_REPO" type:"path" placeholder:"PATH"`
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

// DaemonCmd is a command group. `knowledge-tools daemon` with no subcommand still runs the loop
// (default:"withargs" on Run) so the installed autostart units — whose ExecStart is `<bin> daemon`
// — keep working without a re-install. The lifecycle subcommands (restart/start/stop/status) manage
// the OS unit; restart is the smooth upgrade path after swapping the binary.
type DaemonCmd struct {
	Run     DaemonRunCmd     `cmd:"" default:"withargs" help:"Run the long-running vault daemon (default; what the autostart unit invokes)."`
	Restart DaemonRestartCmd `cmd:"" help:"Re-apply the autostart unit and restart the running daemon (use after upgrading the binary)."`
	Start   DaemonStartCmd   `cmd:"" help:"Start the daemon unit."`
	Stop    DaemonStopCmd    `cmd:"" help:"Stop the daemon unit (without removing it)."`
	Status  DaemonStatusCmd  `cmd:"" help:"Show the daemon unit state and running-vs-installed version."`
}

type DaemonRunCmd struct{}

func (c *DaemonRunCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return daemon.Run(ctx, cfg, version)
}

type DaemonRestartCmd struct{}

func (c *DaemonRestartCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	return service.Restart(service.Options{Cfg: cfg})
}

type DaemonStartCmd struct{}

func (c *DaemonStartCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	return service.Start(cfg)
}

type DaemonStopCmd struct{}

func (c *DaemonStopCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	return service.Stop(cfg)
}

type DaemonStatusCmd struct{}

func (c *DaemonStatusCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	printDaemonStatus(cfg)
	return nil
}

// runOverrides is the shared per-run model/effort flag pair, embedded (kong flattens anonymous
// embeds) into each job command so the flag definitions live in one place. overrides() maps them to
// the jobs layer.
type runOverrides struct {
	Model  string `help:"Override the model for this run (else KNOWLEDGE_*_MODEL / harness default)." placeholder:"MODEL"`
	Effort string `help:"Override reasoning effort for this run (harness-specific; else env / default)." placeholder:"EFFORT"`
}

func (o runOverrides) overrides() jobs.Overrides {
	return jobs.Overrides{Model: o.Model, Effort: o.Effort}
}

type CompileCmd struct {
	Manual bool `help:"Treat as an on-demand compile (cooldown-throttled)."`
	runOverrides
}

func (c *CompileCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.Compile(ctx, cfg, c.Manual, c.overrides()))
}

type SynthesizeCmd struct {
	runOverrides
}

func (c *SynthesizeCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.RunIssueJob(ctx, cfg, jobs.JobSynthesize, c.overrides()))
}

type ResolveCmd struct {
	runOverrides
}

func (c *ResolveCmd) Run(g *Globals) error {
	cfg, err := g.load()
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return ignoreLocked(jobs.RunIssueJob(ctx, cfg, jobs.JobResolve, c.overrides()))
}

type InitCmd struct {
	Dir string `arg:"" optional:"" help:"Target vault dir (default: --vault / KNOWLEDGE_REPO)." type:"path"`
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
		return fmt.Errorf("no target — pass a dir or set --vault / KNOWLEDGE_REPO")
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
