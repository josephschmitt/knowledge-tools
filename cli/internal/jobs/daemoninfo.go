package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

// daemon.json records the running daemon's build version (plus pid + start time) under
// inbox/.compile/. It is deliberately a *separate* file from status.json / schedules.json — those
// are a fixed contract the MCP/REST service reads, whereas this is CLI-only state the service
// ignores. It lets `knowledge-tools status` detect a stale daemon (one still running an older
// binary than the one now installed) and nudge a `daemon restart`.

// DaemonInfo is the payload of inbox/.compile/daemon.json.
type DaemonInfo struct {
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

func daemonInfoFile(cfg *config.Config) string {
	return filepath.Join(cfg.CompileDir(), "daemon.json")
}

// WriteDaemonInfo stamps inbox/.compile/daemon.json with the daemon's version, pid, and start
// time. Best-effort: never returns an error so daemon startup isn't gated on it (like
// RefreshSchedules). Reuses the atomic writer so a reader never sees a half-written file.
func WriteDaemonInfo(cfg *config.Config, version string) {
	_ = writeJSONAtomic(daemonInfoFile(cfg), DaemonInfo{
		Version:   version,
		PID:       os.Getpid(),
		StartedAt: time.Now().Format(time.RFC3339),
	})
}

// ReadDaemonInfo returns the recorded daemon info, or nil if no daemon has written it yet (or it's
// unreadable). Used by the status command to compare the running daemon's version against the
// installed binary's.
func ReadDaemonInfo(cfg *config.Config) *DaemonInfo {
	data, err := os.ReadFile(daemonInfoFile(cfg))
	if err != nil {
		return nil
	}
	var info DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}
