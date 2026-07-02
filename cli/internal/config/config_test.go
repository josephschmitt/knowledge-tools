package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	contents := `# a comment
KNOWLEDGE_REPO=/vault/path
QUOTED="quoted value"
SQUOTED='single'

  KNOWLEDGE_INSTANCE = work
ALREADY_SET=fromfile
`
	if err := os.WriteFile(env, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	// A real env var must win over the file.
	t.Setenv("ALREADY_SET", "fromenv")
	// Ensure the keys we assert on start unset.
	_ = os.Unsetenv("KNOWLEDGE_REPO")
	_ = os.Unsetenv("QUOTED")
	_ = os.Unsetenv("SQUOTED")
	_ = os.Unsetenv("KNOWLEDGE_INSTANCE")

	if err := LoadDotenv(env); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}

	cases := map[string]string{
		"KNOWLEDGE_REPO":     "/vault/path",
		"QUOTED":             "quoted value", // one layer of quotes stripped
		"SQUOTED":            "single",
		"KNOWLEDGE_INSTANCE": "work", // key whitespace trimmed
		"ALREADY_SET":        "fromenv",
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadDotenvMissingFileOK(t *testing.T) {
	if err := LoadDotenv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing .env should be a no-op, got %v", err)
	}
}

func TestValidateInstance(t *testing.T) {
	good := []string{"default", "work", "my-vault", "v_2"}
	bad := []string{"", "has space", "slash/x", "dot.dot", "amp&"}
	for _, s := range good {
		if err := ValidateInstance(s); err != nil {
			t.Errorf("ValidateInstance(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateInstance(s); err == nil {
			t.Errorf("ValidateInstance(%q) expected error", s)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{
		"KNOWLEDGE_INSTANCE", "KNOWLEDGE_REPO", "CLAUDE_BIN", "KNOWLEDGE_COMPILE_COOLDOWN",
		"KNOWLEDGE_REVIEW_CHANNEL", "KNOWLEDGE_VAULT_LOCK", "KNOWLEDGE_COMPILE_SCHEDULE",
		"KNOWLEDGE_SYNTHESIZE_SCHEDULE", "KNOWLEDGE_RESOLVE_SCHEDULE",
	} {
		_ = os.Unsetenv(k)
	}

	cfg, err := Load("", "/my/vault")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != DefaultInstance {
		t.Errorf("Instance = %q, want %q", cfg.Instance, DefaultInstance)
	}
	if cfg.Repo != "/my/vault" {
		t.Errorf("Repo = %q", cfg.Repo)
	}
	if cfg.CompileCooldown != DefaultCompileCooldown {
		t.Errorf("CompileCooldown = %d", cfg.CompileCooldown)
	}
	if cfg.CompileSchedule != DefaultCompileSchedule {
		t.Errorf("CompileSchedule = %q", cfg.CompileSchedule)
	}
	wantLock := "vault-default.lock"
	if filepath.Base(cfg.VaultLock) != wantLock {
		t.Errorf("VaultLock base = %q, want %q", filepath.Base(cfg.VaultLock), wantLock)
	}
}

func TestLoadInstanceFromEnvAndFlagOverride(t *testing.T) {
	t.Setenv("KNOWLEDGE_INSTANCE", "fromenv")
	// Flag (explicit arg) wins over env.
	cfg, err := Load("fromflag", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != "fromflag" {
		t.Errorf("Instance = %q, want fromflag", cfg.Instance)
	}
	// Empty arg falls back to env.
	cfg2, err := Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Instance != "fromenv" {
		t.Errorf("Instance = %q, want fromenv", cfg2.Instance)
	}
}

func TestRequireRepo(t *testing.T) {
	if err := (&Config{Repo: ""}).RequireRepo(); err == nil {
		t.Error("empty repo should error")
	}
	if err := (&Config{Repo: "/x"}).RequireRepo(); err != nil {
		t.Errorf("non-empty repo should be ok, got %v", err)
	}
}

func TestJobModel(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		override string
		want     string
	}{
		{"override wins over everything", Config{Agent: "codex", CompileModel: "perjob", AgentModel: "agentwide"}, "override", "override"},
		{"per-job env beats agent-wide", Config{Agent: "codex", CompileModel: "perjob", AgentModel: "agentwide"}, "", "perjob"},
		{"agent-wide env when no per-job", Config{Agent: "codex", AgentModel: "agentwide"}, "", "agentwide"},
		{"claude defaults to opus when empty", Config{Agent: "claude"}, "", "opus"},
		{"empty agent defaults to opus (claude)", Config{Agent: ""}, "", "opus"},
		{"override beats claude opus default", Config{Agent: "claude"}, "override", "override"},
		{"non-claude empty stays empty", Config{Agent: "codex"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.JobModel("compile", tt.override); got != tt.want {
				t.Errorf("JobModel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJobEffort(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		override string
		want     string
	}{
		{"override wins over everything", Config{SynthesizeEffort: "perjob", AgentEffort: "agentwide"}, "high", "high"},
		{"per-job env beats agent-wide", Config{SynthesizeEffort: "perjob", AgentEffort: "agentwide"}, "", "perjob"},
		{"agent-wide env when no per-job", Config{AgentEffort: "agentwide"}, "", "agentwide"},
		{"empty when nothing set (no default)", Config{Agent: "claude"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.JobEffort("synthesize", tt.override); got != tt.want {
				t.Errorf("JobEffort = %q, want %q", got, tt.want)
			}
		})
	}
}
