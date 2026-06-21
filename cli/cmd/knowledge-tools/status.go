package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
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

	fmt.Printf("\ndaemon: %s\n", daemonState(cfg.Instance))
	return nil
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
	return execRun(name, args...) == nil
}

func execRun(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func indent(s string) string {
	out := ""
	for _, line := range splitLines(s) {
		out += "  " + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
