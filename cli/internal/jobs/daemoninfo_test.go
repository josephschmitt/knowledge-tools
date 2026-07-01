package jobs

import (
	"os"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

func TestDaemonInfoRoundTrip(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{Repo: repo, Instance: "test"}

	// No daemon has written yet → nil, not an error.
	if got := ReadDaemonInfo(cfg); got != nil {
		t.Fatalf("ReadDaemonInfo before write = %+v, want nil", got)
	}

	WriteDaemonInfo(cfg, "cli/v1.2.3")

	got := ReadDaemonInfo(cfg)
	if got == nil {
		t.Fatal("ReadDaemonInfo after write = nil, want info")
	}
	if got.Version != "cli/v1.2.3" {
		t.Errorf("version = %q, want %q", got.Version, "cli/v1.2.3")
	}
	if got.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", got.PID, os.Getpid())
	}
	if got.StartedAt == "" {
		t.Error("started_at is empty")
	}
}

func TestReadDaemonInfoIgnoresGarbage(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{Repo: repo, Instance: "test"}
	if err := os.MkdirAll(cfg.CompileDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonInfoFile(cfg), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadDaemonInfo(cfg); got != nil {
		t.Fatalf("ReadDaemonInfo on garbage = %+v, want nil", got)
	}
}
