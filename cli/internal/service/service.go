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
	if cfg.ClaudeBin != "" {
		env = append(env, envKV{"CLAUDE_BIN", cfg.ClaudeBin})
	}
	if cfg.ReviewChannel != "" {
		env = append(env, envKV{"KNOWLEDGE_REVIEW_CHANNEL", cfg.ReviewChannel})
	}
	if cfg.GithubRepo != "" {
		env = append(env, envKV{"KNOWLEDGE_GITHUB_REPO", cfg.GithubRepo})
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
