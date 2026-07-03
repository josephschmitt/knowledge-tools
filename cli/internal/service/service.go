// Package service installs and removes the single OS autostart unit that keeps the
// knowledge-tools daemon alive for one vault instance — a systemd user service on Linux, a launchd
// LaunchAgent on macOS. This replaces install.sh / uninstall.sh and all the per-job timer / path /
// plist generation: the daemon owns scheduling now, so install only has to register one unit.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
)

// envKV is one environment entry baked into the daemon unit.
type envKV struct{ k, v string }

// commonEnv is the KNOWLEDGE_* set both the systemd env file and the launchd plist carry for the
// daemon — defined once so the two renderers can't drift (e.g. when a new knob is added). The
// OS-specific extras (systemd's KNOWLEDGE_INSTANCE via Environment=%i; launchd's KNOWLEDGE_INSTANCE
// + PATH) are added by each renderer.
func commonEnv(cfg *config.Config) []envKV {
	env := []envKV{
		{"KNOWLEDGE_REPO", cfg.Repo},
		{"KNOWLEDGE_COMPILE_SCHEDULE", cfg.CompileSchedule},
		{"KNOWLEDGE_SYNTHESIZE_SCHEDULE", cfg.SynthesizeSchedule},
		{"KNOWLEDGE_RESOLVE_SCHEDULE", cfg.ResolveSchedule},
		{"KNOWLEDGE_COMPILE_COOLDOWN", strconv.Itoa(cfg.CompileCooldown)},
	}
	// The agent harness selection + its model/effort/bin knobs — only the ones the user set, so a
	// default-claude deployment's unit stays as lean as before.
	for _, kv := range []envKV{
		{"KNOWLEDGE_AGENT", cfg.Agent},
		{"KNOWLEDGE_AGENT_BIN", cfg.AgentBin},
		{"KNOWLEDGE_AGENT_CMD", cfg.AgentCmd},
		{"KNOWLEDGE_AGENT_MODEL", cfg.AgentModel},
		{"KNOWLEDGE_AGENT_EFFORT", cfg.AgentEffort},
		{"KNOWLEDGE_COMPILE_MODEL", cfg.CompileModel},
		{"KNOWLEDGE_SYNTHESIZE_MODEL", cfg.SynthesizeModel},
		{"KNOWLEDGE_RESOLVE_MODEL", cfg.ResolveModel},
		{"KNOWLEDGE_COMPILE_EFFORT", cfg.CompileEffort},
		{"KNOWLEDGE_SYNTHESIZE_EFFORT", cfg.SynthesizeEffort},
		{"KNOWLEDGE_RESOLVE_EFFORT", cfg.ResolveEffort},
	} {
		// KNOWLEDGE_AGENT defaults to "claude"; omit it from the unit when it's the default so an
		// unconfigured deployment's env file is unchanged.
		if kv.v == "" || (kv.k == "KNOWLEDGE_AGENT" && kv.v == config.DefaultAgent) {
			continue
		}
		env = append(env, kv)
	}
	if cfg.ReviewChannel != "" {
		env = append(env, envKV{"KNOWLEDGE_REVIEW_CHANNEL", cfg.ReviewChannel})
	}
	if cfg.GithubRepo != "" {
		env = append(env, envKV{"KNOWLEDGE_GITHUB_REPO", cfg.GithubRepo})
	}
	// Site-rebuild wiring — without these in the unit, the daemon's jobs never POST /rebuild after a
	// commit and the published site goes stale (config.Load parses them, CommitAndPush uses them).
	if cfg.SiteRebuildURL != "" {
		env = append(env, envKV{"KNOWLEDGE_SITE_REBUILD_URL", cfg.SiteRebuildURL})
	}
	if cfg.SiteRebuildToken != "" {
		env = append(env, envKV{"KNOWLEDGE_SITE_REBUILD_TOKEN", cfg.SiteRebuildToken})
	}
	return env
}

// Options carries everything install/uninstall needs beyond the resolved config.
type Options struct {
	Cfg     *config.Config
	BinPath string // absolute path to the knowledge-tools binary; resolved from os.Executable if empty
}

// Install registers the daemon autostart unit for the instance and starts it.
func Install(opts Options) error {
	bin, err := resolveBin(opts.BinPath)
	if err != nil {
		return err
	}
	opts.BinPath = bin
	if err := opts.Cfg.RequireRepo(); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "linux":
		err = installSystemd(opts)
	case "darwin":
		err = installLaunchd(opts)
	default:
		return fmt.Errorf("unsupported OS %q — need Linux (systemd) or macOS (launchd)", runtime.GOOS)
	}
	if err != nil {
		return err
	}
	// Seed the schedule snapshot so next-run times show before the first tick (install.sh:208-212).
	jobs.RefreshSchedules(opts.Cfg)
	return nil
}

// Uninstall removes the instance's daemon unit. Idempotent (a no-op if not installed) and needs no
// KNOWLEDGE_REPO — it touches nothing inside the vault. Ports scripts/uninstall.sh.
func Uninstall(cfg *config.Config) error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemd(cfg)
	case "darwin":
		return uninstallLaunchd(cfg)
	default:
		return fmt.Errorf("unsupported OS %q — need Linux (systemd) or macOS (launchd)", runtime.GOOS)
	}
}

// Restart re-applies the autostart unit (picking up any new features/knobs baked into the unit
// template or per-instance env by a newer binary) and restarts the running daemon onto the current
// binary. This is the smooth upgrade path: after installing a new binary, `daemon restart` rolls
// the live daemon forward — Install alone won't, because `enable --now` (Linux) does not restart an
// already-running unit.
func Restart(opts Options) error {
	// Install rewrites the unit + per-instance env from current config and runs daemon-reload — it
	// *is* the "install anything new" step. It also resolves BinPath and requires KNOWLEDGE_REPO.
	if err := Install(opts); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "linux":
		// enable --now (inside Install) starts a stopped unit but won't restart a running one; force
		// it so the daemon re-execs onto the new binary.
		fmt.Printf("Restarting %s\n", daemonUnit(opts.Cfg.Instance))
		return run("systemctl", "--user", "restart", daemonUnit(opts.Cfg.Instance))
	case "darwin":
		// installLaunchd already did bootout + bootstrap, i.e. a full re-exec onto the new binary.
		return nil
	default:
		return fmt.Errorf("unsupported OS %q — need Linux (systemd) or macOS (launchd)", runtime.GOOS)
	}
}

// Start starts the (already-installed) daemon unit. A no-op-ish error if it was never installed.
func Start(cfg *config.Config) error {
	switch runtime.GOOS {
	case "linux":
		fmt.Printf("Starting %s\n", daemonUnit(cfg.Instance))
		return run("systemctl", "--user", "start", daemonUnit(cfg.Instance))
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		label := daemonLabel(cfg.Instance)
		fmt.Printf("Starting %s\n", label)
		// bootstrap re-registers the agent (undoing Stop's bootout) and starts it. If it's already
		// bootstrapped, bootstrap errors — fall back to kickstart to ensure it's actually running, so
		// Start stays roughly idempotent like `systemctl start`.
		if err := run("launchctl", "bootstrap", "gui/"+uid, daemonPlistPath(cfg.Instance)); err != nil {
			return run("launchctl", "kickstart", "gui/"+uid+"/"+label)
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS %q — need Linux (systemd) or macOS (launchd)", runtime.GOOS)
	}
}

// Stop stops the running daemon unit without removing it (unlike Uninstall). On macOS a plain
// `launchctl kill` wouldn't stick — the plist's KeepAlive makes launchd immediately respawn the
// process — so Stop boots the agent out of launchd (stopped + untracked) while leaving the plist in
// place, so Start can re-bootstrap it.
func Stop(cfg *config.Config) error {
	switch runtime.GOOS {
	case "linux":
		fmt.Printf("Stopping %s\n", daemonUnit(cfg.Instance))
		return run("systemctl", "--user", "stop", daemonUnit(cfg.Instance))
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		label := daemonLabel(cfg.Instance)
		fmt.Printf("Stopping %s\n", label)
		return run("launchctl", "bootout", "gui/"+uid+"/"+label)
	default:
		return fmt.Errorf("unsupported OS %q — need Linux (systemd) or macOS (launchd)", runtime.GOOS)
	}
}

// resolveBin returns the absolute path to the knowledge-tools binary to bake into the unit.
func resolveBin(bin string) (string, error) {
	if bin != "" {
		return bin, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not resolve the knowledge-tools binary path: %w", err)
	}
	return exe, nil
}

// run executes a command, streaming output to the user's stdout/stderr, and returns its error.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runQuiet executes a command discarding output, returning whether it succeeded.
func runQuiet(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}
