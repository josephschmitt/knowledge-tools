package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

// systemd unit names. One shared template, instantiated per vault by KNOWLEDGE_INSTANCE.
const daemonTemplate = "knowledge-tools-daemon@.service"

func daemonUnit(instance string) string { return "knowledge-tools-daemon@" + instance + ".service" }

func systemdUnitDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func knowledgeConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "knowledge-tools")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "knowledge-tools")
}

// daemonServiceContents is the shared service template. EnvironmentFile pulls each vault's
// KNOWLEDGE_REPO + overrides from its per-instance env file; the optional gh.env (leading '-')
// carries gh auth for the github review channel. PATH includes the usual gh/nix locations.
func daemonServiceContents(bin string) string {
	return fmt.Sprintf(`[Unit]
Description=knowledge-tools daemon for the %%i vault (scheduler + on-demand compile watcher)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=%%h/.config/knowledge-tools/%%i.env
EnvironmentFile=-%%h/.config/knowledge-tools/gh.env
Environment=KNOWLEDGE_INSTANCE=%%i
Environment=PATH=%%h/.nix-profile/bin:%%h/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=%s daemon
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, bin)
}

// instanceEnvContents is the per-instance env file the shared template reads.
func instanceEnvContents(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("# Written by `knowledge-tools install` for instance " + cfg.Instance + ". Re-run install to update.\n")
	fmt.Fprintf(&b, "KNOWLEDGE_REPO=%s\n", cfg.Repo)
	fmt.Fprintf(&b, "KNOWLEDGE_COMPILE_SCHEDULE=%s\n", cfg.CompileSchedule)
	fmt.Fprintf(&b, "KNOWLEDGE_SYNTHESIZE_SCHEDULE=%s\n", cfg.SynthesizeSchedule)
	fmt.Fprintf(&b, "KNOWLEDGE_RESOLVE_SCHEDULE=%s\n", cfg.ResolveSchedule)
	fmt.Fprintf(&b, "KNOWLEDGE_COMPILE_COOLDOWN=%d\n", cfg.CompileCooldown)
	if cfg.ClaudeBin != "" {
		fmt.Fprintf(&b, "CLAUDE_BIN=%s\n", cfg.ClaudeBin)
	}
	if cfg.ReviewChannel != "" {
		fmt.Fprintf(&b, "KNOWLEDGE_REVIEW_CHANNEL=%s\n", cfg.ReviewChannel)
	}
	if cfg.GithubRepo != "" {
		fmt.Fprintf(&b, "KNOWLEDGE_GITHUB_REPO=%s\n", cfg.GithubRepo)
	}
	if cfg.SiteEnable {
		b.WriteString("KNOWLEDGE_SITE_ENABLE=true\n")
		fmt.Fprintf(&b, "KNOWLEDGE_QUARTZ_REF=%s\n", cfg.QuartzRef)
		fmt.Fprintf(&b, "KNOWLEDGE_SITE_ROOT=%s\n", cfg.SiteRoot)
	}
	return b.String()
}

func installSystemd(opts Options) error {
	cfg := opts.Cfg
	if !runQuiet("systemctl", "--user", "show-environment") {
		return fmt.Errorf("systemd user instance isn't available — are you on the host as your user?")
	}

	unitDir := systemdUnitDir()
	configDir := knowledgeConfigDir()
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	fmt.Printf("Installing vault '%s' (systemd)\n", cfg.Instance)

	// Remove any pre-daemon (bash-era) units for this instance so they don't double-run.
	cleanupLegacySystemd(unitDir, cfg.Instance)

	// Shared service template + per-instance env file (0600 — may carry a github repo).
	if err := os.WriteFile(filepath.Join(unitDir, daemonTemplate), []byte(daemonServiceContents(opts.BinPath)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir, cfg.Instance+".env"), []byte(instanceEnvContents(cfg)), 0o600); err != nil {
		return err
	}

	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "enable", "--now", daemonUnit(cfg.Instance)); err != nil {
		return err
	}
	// Allow the daemon to run while logged out.
	if !runQuiet("loginctl", "enable-linger", currentUser()) {
		fmt.Println("  note: could not enable linger; the daemon will only run while you're logged in.")
	}

	fmt.Printf("Done. The daemon is running as %s.\n", daemonUnit(cfg.Instance))
	fmt.Printf("  status: systemctl --user status %s\n", daemonUnit(cfg.Instance))
	fmt.Printf("  logs:   journalctl --user -u %s -f\n", daemonUnit(cfg.Instance))
	return nil
}

func uninstallSystemd(cfg *config.Config) error {
	if !runQuiet("systemctl", "--user", "show-environment") {
		return fmt.Errorf("systemd user instance isn't available — are you on the host as your user?")
	}
	unitDir := systemdUnitDir()
	configDir := knowledgeConfigDir()
	instanceEnv := filepath.Join(configDir, cfg.Instance+".env")

	fmt.Printf("Uninstalling vault '%s' (systemd)\n", cfg.Instance)

	// Stop + disable this instance's daemon (ignore whatever's already gone). Also clean any
	// leftover pre-daemon units for the instance.
	runQuiet("systemctl", "--user", "disable", "--now", daemonUnit(cfg.Instance))
	cleanupLegacySystemd(unitDir, cfg.Instance)

	removed := false
	if remove(instanceEnv) {
		fmt.Printf("  removed %s\n", tildify(instanceEnv))
		removed = true
	}

	// Last instance? If no per-instance env files remain, the shared template is orphaned — remove
	// it too (and rmdir the config dir if it's emptied; gh.env etc. keep it).
	if lastInstance(configDir) {
		tmpl := filepath.Join(unitDir, daemonTemplate)
		if remove(tmpl) {
			fmt.Printf("  removed %s (last instance — shared template)\n", daemonTemplate)
			removed = true
		}
		if os.Remove(configDir) == nil {
			fmt.Printf("  removed %s (empty)\n", tildify(configDir))
		}
	}

	if removed {
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		fmt.Println("Done.")
	} else {
		fmt.Printf("Nothing to remove for instance '%s'.\n", cfg.Instance)
	}
	fmt.Println("(Left untouched: the vault itself — inbox/, wiki/, outputs/ — and linger.)")
	return nil
}

// cleanupLegacySystemd disables + removes the pre-daemon per-job units (timers, the compile path
// watcher, and — when nothing else needs them — the shared per-job service templates) for the
// instance, plus the original non-instanced units when migrating the default vault. This is the
// one-time cutover from the bash install.
func cleanupLegacySystemd(unitDir, instance string) {
	legacy := []string{
		"knowledge-compile@" + instance + ".timer",
		"knowledge-compile@" + instance + ".path",
		"knowledge-synthesize@" + instance + ".timer",
		"knowledge-resolve@" + instance + ".timer",
	}
	if instance == config.DefaultInstance {
		// The original single-vault host used non-instanced units.
		legacy = append(legacy,
			"knowledge-compile.timer", "knowledge-compile.path",
			"knowledge-synthesize.timer", "knowledge-resolve.timer",
		)
	}
	for _, u := range legacy {
		runQuiet("systemctl", "--user", "disable", "--now", u)
		remove(filepath.Join(unitDir, u))
	}
	// Drop the orphaned shared per-job service templates if no instanced compile timer remains.
	if !anyMatch(unitDir, "knowledge-compile@*.timer") {
		for _, t := range []string{"knowledge-compile@.service", "knowledge-synthesize@.service", "knowledge-resolve@.service"} {
			remove(filepath.Join(unitDir, t))
		}
	}
}

// lastInstance reports whether no per-instance env files remain in configDir (gh.env doesn't count
// — it's shared auth, not an instance). Drives the shared-template cleanup.
func lastInstance(configDir string) bool {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".env") || name == "gh.env" {
			continue
		}
		return false
	}
	return true
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return strconv.Itoa(os.Getuid())
}
