package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

func launchAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func launchdLogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "knowledge-tools")
}

func daemonLabel(instance string) string { return "com.knowledge-tools.daemon." + instance }

// launchdPATH is the PATH baked into the agent (launchd starts with a bare PATH). Absolute home —
// plists don't expand $HOME.
func launchdPATH() string {
	home, _ := os.UserHomeDir()
	return strings.Join([]string{
		"/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".nix-profile", "bin"),
		"/usr/sbin", "/usr/bin", "/sbin", "/bin",
	}, ":")
}

// plistContents renders the LaunchAgent. KeepAlive + RunAtLoad keep the daemon alive (restart on
// crash, start at login); there's no schedule block or WatchPaths — the daemon owns both.
func plistContents(opts Options) string {
	cfg := opts.Cfg
	logFile := filepath.Join(launchdLogDir(), cfg.Instance+".log")

	// launchd starts with a bare PATH and has no Environment=%i, so it carries KNOWLEDGE_INSTANCE +
	// PATH on top of the shared set.
	env := append([]envKV{
		{"KNOWLEDGE_INSTANCE", cfg.Instance},
		{"PATH", launchdPATH()},
	}, commonEnv(cfg)...)

	var envXML strings.Builder
	for _, kv := range env {
		fmt.Fprintf(&envXML, "    <key>%s</key>\n    <string>%s</string>\n", xmlEscape(kv.k), xmlEscape(kv.v))
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>--instance</string>
    <string>%s</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
%s  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlEscape(daemonLabel(cfg.Instance)), xmlEscape(opts.BinPath), xmlEscape(cfg.Instance),
		envXML.String(), xmlEscape(logFile), xmlEscape(logFile))
}

func installLaunchd(opts Options) error {
	cfg := opts.Cfg
	laDir := launchAgentsDir()
	logDir := launchdLogDir()
	if err := os.MkdirAll(laDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	fmt.Printf("Installing vault '%s' (launchd)\n", cfg.Instance)

	cleanupLegacyLaunchd(laDir, logDir, cfg.Instance)

	label := daemonLabel(cfg.Instance)
	dest := filepath.Join(laDir, label+".plist")
	if err := os.WriteFile(dest, []byte(plistContents(opts)), 0o644); err != nil {
		return err
	}

	uid := strconv.Itoa(os.Getuid())
	// Reload: bootout any prior instance, then bootstrap (fall back to load -w on older macOS).
	runQuiet("launchctl", "bootout", "gui/"+uid+"/"+label)
	if err := run("launchctl", "bootstrap", "gui/"+uid, dest); err != nil {
		if err := run("launchctl", "load", "-w", dest); err != nil {
			return err
		}
	}

	fmt.Printf("Done. The daemon is running as %s.\n", label)
	fmt.Printf("  logs: %s\n", tildify(filepath.Join(logDir, cfg.Instance+".log")))
	fmt.Println("  note: LaunchAgents only run while you're logged in (no linger on macOS).")
	return nil
}

func uninstallLaunchd(cfg *config.Config) error {
	laDir := launchAgentsDir()
	logDir := launchdLogDir()
	uid := strconv.Itoa(os.Getuid())

	fmt.Printf("Uninstalling vault '%s' (launchd)\n", cfg.Instance)

	label := daemonLabel(cfg.Instance)
	dest := filepath.Join(laDir, label+".plist")
	logFile := filepath.Join(logDir, cfg.Instance+".log")

	runQuiet("launchctl", "bootout", "gui/"+uid+"/"+label)
	cleanupLegacyLaunchd(laDir, logDir, cfg.Instance)

	removed := false
	if remove(dest) {
		fmt.Printf("  removed %s\n", tildify(dest))
		removed = true
	}
	if remove(logFile) {
		fmt.Printf("  removed %s\n", tildify(logFile))
		removed = true
	}

	// Last instance? Key off surviving daemon plists (not an empty logs dir — agents only write
	// once they fire). Drop the now-orphaned logs dir.
	if !anyMatch(laDir, "com.knowledge-tools.daemon.*.plist") {
		if os.Remove(logDir) == nil {
			fmt.Printf("  removed %s (empty)\n", tildify(logDir))
		}
	}

	if removed {
		fmt.Println("Done.")
	} else {
		fmt.Printf("Nothing to remove for instance '%s'.\n", cfg.Instance)
	}
	fmt.Println("(Left untouched: the vault itself — inbox/, library/, outputs/.)")
	return nil
}

// cleanupLegacyLaunchd removes the pre-daemon per-job agents (compile/synthesize/resolve) and their
// logs for the instance — the one-time cutover from the bash install.
func cleanupLegacyLaunchd(laDir, logDir, instance string) {
	uid := strconv.Itoa(os.Getuid())
	for _, job := range []string{"compile", "synthesize", "resolve"} {
		label := "com.knowledge-tools." + job + "." + instance
		runQuiet("launchctl", "bootout", "gui/"+uid+"/"+label)
		remove(filepath.Join(laDir, label+".plist"))
		remove(filepath.Join(logDir, instance+"-"+job+".log"))
	}
}
