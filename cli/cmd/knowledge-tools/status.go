package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// vaultErrLocked aliases the lock sentinel so main.go can compare without importing vault directly.
var vaultErrLocked = vault.ErrLocked

// printStatus prints the compile + schedule snapshots (the same files the MCP service reads) and
// the daemon unit's running state.
func printStatus(cfg *config.Config) error {
	fmt.Printf("instance: %s\n", cfg.Instance)
	fmt.Printf("repo:     %s\n", cfg.Repo)

	compileDir := cfg.CompileDir()
	printFile := func(label, path string) {
		fmt.Printf("\n%s (%s):\n", label, path)
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("  (none yet)")
			return
		}
		fmt.Print(indent(string(data)))
	}
	printFile("compile status", filepath.Join(compileDir, "status.json"))
	printFile("schedules", filepath.Join(compileDir, "schedules.json"))

	fmt.Println()
	printDaemonStatus(cfg)
	return nil
}

// printDaemonStatus prints the daemon unit's running state and, when the running daemon reports a
// different build version than this (installed) binary, a nudge to restart. Shared by `status` and
// `daemon status` so the two never drift.
func printDaemonStatus(cfg *config.Config) {
	fmt.Printf("daemon: %s\n", daemonState(cfg.Instance))

	// Only meaningful once a daemon has recorded its version. Skip the compare for unversioned local
	// builds ("dev") on either side — a dev binary vs a dev daemon isn't a real staleness signal.
	info := jobs.ReadDaemonInfo(cfg)
	if info == nil || info.Version == "" {
		return
	}
	fmt.Printf("  running version: %s   installed: %s\n", info.Version, version)
	if version != "dev" && info.Version != "dev" && info.Version != version {
		fmt.Println("  → a different binary is installed; run `knowledge-tools daemon restart` to upgrade the daemon.")
	}
}

// daemonState reports whether the daemon autostart unit is active, per OS.
func daemonState(instance string) string {
	switch runtime.GOOS {
	case "linux":
		unit := "knowledge-tools-daemon@" + instance + ".service"
		if runOK("systemctl", "--user", "is-active", "--quiet", unit) {
			return "running (" + unit + ")"
		}
		return "not running (" + unit + ")"
	case "darwin":
		label := "com.knowledge-tools.daemon." + instance
		if runOK("launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+label) {
			return "loaded (" + label + ")"
		}
		return "not loaded (" + label + ")"
	default:
		return "unknown on this OS"
	}
}

func runOK(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}

// indent prefixes every line of s with two spaces.
func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}
