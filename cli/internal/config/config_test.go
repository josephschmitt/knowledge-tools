package config

import (
	"os"
	"path/filepath"
	"strings"
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
	// The raw field is an override only (empty by default); the effective schedule comes from
	// JobSchedule, which falls back to the built-in default.
	if cfg.CompileSchedule != "" {
		t.Errorf("CompileSchedule override = %q, want empty by default", cfg.CompileSchedule)
	}
	if got := cfg.JobSchedule("compile"); got != DefaultCompileSchedule {
		t.Errorf("JobSchedule(compile) = %q, want default %q", got, DefaultCompileSchedule)
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

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	contents := `# a comment
KNOWLEDGE_REPO=/vault/path
QUOTED="quoted value"
SQUOTED='single'

  KNOWLEDGE_INSTANCE = work
NOEQ
=novalue
`
	if err := os.WriteFile(env, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := parseEnvFile(env)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	want := map[string]string{
		"KNOWLEDGE_REPO":     "/vault/path",
		"QUOTED":             "quoted value",
		"SQUOTED":            "single",
		"KNOWLEDGE_INSTANCE": "work",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("%s = %q, want %q", k, kv[k], v)
		}
	}
	if _, ok := kv["NOEQ"]; ok {
		t.Error("line without '=' should be skipped")
	}
	if len(kv) != len(want) {
		t.Errorf("parsed %d keys, want %d: %v", len(kv), len(want), kv)
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	kv, err := parseEnvFile(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil {
		t.Errorf("missing file should be nil error, got %v", err)
	}
	if kv != nil {
		t.Errorf("missing file should return nil map, got %v", kv)
	}
}

// writeVaultConfig creates <repo>/.knowledge/config.yaml with the given YAML body and returns repo.
func writeVaultConfig(t *testing.T, body string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// vaultEnvKeys are the KNOWLEDGE_* knobs the vault config.yaml can also supply (model/effort +
// schedules). Tests clear them so the ambient environment doesn't pollute vault-tier assertions.
var vaultEnvKeys = []string{
	"KNOWLEDGE_AGENT_MODEL", "KNOWLEDGE_AGENT_EFFORT",
	"KNOWLEDGE_COMPILE_MODEL", "KNOWLEDGE_SYNTHESIZE_MODEL", "KNOWLEDGE_RESOLVE_MODEL",
	"KNOWLEDGE_COMPILE_EFFORT", "KNOWLEDGE_SYNTHESIZE_EFFORT", "KNOWLEDGE_RESOLVE_EFFORT",
	"KNOWLEDGE_COMPILE_SCHEDULE", "KNOWLEDGE_SYNTHESIZE_SCHEDULE", "KNOWLEDGE_RESOLVE_SCHEDULE",
}

// clearModelEffortEnv unsets every vault-settable env knob so a test isn't polluted by the ambient
// environment (they're not in the harness default-unset list).
func clearModelEffortEnv(t *testing.T) {
	t.Helper()
	for _, k := range vaultEnvKeys {
		t.Setenv(k, "") // register for restore
		_ = os.Unsetenv(k)
	}
}

func TestLoadVaultConfigAllowlist(t *testing.T) {
	clearModelEffortEnv(t)
	// The vault file sets allowlisted knobs AND unrepresentable infra keys (as stray top-level YAML
	// and an unknown job) — only the structurally-declared fields for known jobs may land.
	repo := writeVaultConfig(t, `defaults:
  effort: high
jobs:
  synthesize:
    model: opus
  evil:
    schedule: "@yearly"
github_repo: evil/repo
site_rebuild_url: http://evil
repo: /somewhere/else
`)
	cfg, err := Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.vault["KNOWLEDGE_SYNTHESIZE_MODEL"] != "opus" {
		t.Errorf("vault[SYNTHESIZE_MODEL] = %q, want opus", cfg.vault["KNOWLEDGE_SYNTHESIZE_MODEL"])
	}
	if cfg.vault["KNOWLEDGE_AGENT_EFFORT"] != "high" {
		t.Errorf("vault[AGENT_EFFORT] = %q, want high", cfg.vault["KNOWLEDGE_AGENT_EFFORT"])
	}
	// An unknown job name is ignored — its schedule must not smuggle a key into the map.
	for k := range cfg.vault {
		if strings.Contains(k, "EVIL") {
			t.Errorf("vault map contains key from unknown job: %q", k)
		}
	}
	// Infra keys are unrepresentable in the struct, so they can't survive into the map or config.
	for _, k := range []string{"KNOWLEDGE_GITHUB_REPO", "KNOWLEDGE_SITE_REBUILD_URL", "KNOWLEDGE_REPO"} {
		if _, ok := cfg.vault[k]; ok {
			t.Errorf("vault map contains forbidden key %q", k)
		}
	}
	if cfg.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, want empty (vault file must not set it)", cfg.GithubRepo)
	}
	if cfg.SiteRebuildURL != "" {
		t.Errorf("SiteRebuildURL = %q, want empty", cfg.SiteRebuildURL)
	}
	// The vault file's repo key is ignored — Repo stays the one passed to Load.
	if cfg.Repo != repo {
		t.Errorf("Repo = %q, want %q (vault file must not override repo)", cfg.Repo, repo)
	}
}

func TestLoadVaultConfigMissingIsNoop(t *testing.T) {
	clearModelEffortEnv(t)
	cfg, err := Load("", t.TempDir()) // repo with no .knowledge/config.yaml
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.vault) != 0 {
		t.Errorf("missing vault file should leave vault map empty, got %v", cfg.vault)
	}
	// claude default still applies with nothing set.
	if got := cfg.JobModel("synthesize", ""); got != "opus" {
		t.Errorf("JobModel with nothing set = %q, want opus (claude default)", got)
	}
}

func TestJobModelPrecedence(t *testing.T) {
	// env per-job beats env agent beats vault per-job beats vault agent beats claude default.
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"env per-job wins over everything", Config{
			Agent: "claude", SynthesizeModel: "envjob", AgentModel: "envagent",
			vault: map[string]string{"KNOWLEDGE_SYNTHESIZE_MODEL": "vaultjob", "KNOWLEDGE_AGENT_MODEL": "vaultagent"},
		}, "envjob"},
		{"env agent beats vault per-job", Config{
			Agent: "claude", AgentModel: "envagent",
			vault: map[string]string{"KNOWLEDGE_SYNTHESIZE_MODEL": "vaultjob", "KNOWLEDGE_AGENT_MODEL": "vaultagent"},
		}, "envagent"},
		{"vault per-job when env empty", Config{
			Agent: "claude",
			vault: map[string]string{"KNOWLEDGE_SYNTHESIZE_MODEL": "vaultjob", "KNOWLEDGE_AGENT_MODEL": "vaultagent"},
		}, "vaultjob"},
		{"vault agent when only it set", Config{
			Agent: "claude",
			vault: map[string]string{"KNOWLEDGE_AGENT_MODEL": "vaultagent"},
		}, "vaultagent"},
		{"claude default when all empty", Config{Agent: "claude"}, "opus"},
		{"non-claude empty when all empty", Config{Agent: "codex"}, ""},
	}
	for _, tc := range cases {
		if got := tc.cfg.JobModel("synthesize", ""); got != tc.want {
			t.Errorf("%s: JobModel = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestJobEffortPrecedence(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"env per-job wins", Config{
			SynthesizeEffort: "envjob", AgentEffort: "envagent",
			vault: map[string]string{"KNOWLEDGE_SYNTHESIZE_EFFORT": "vaultjob", "KNOWLEDGE_AGENT_EFFORT": "vaultagent"},
		}, "envjob"},
		{"env agent beats vault per-job", Config{
			AgentEffort: "envagent",
			vault:       map[string]string{"KNOWLEDGE_SYNTHESIZE_EFFORT": "vaultjob"},
		}, "envagent"},
		{"vault per-job when env empty", Config{
			vault: map[string]string{"KNOWLEDGE_SYNTHESIZE_EFFORT": "vaultjob", "KNOWLEDGE_AGENT_EFFORT": "vaultagent"},
		}, "vaultjob"},
		{"vault agent when only it set", Config{
			vault: map[string]string{"KNOWLEDGE_AGENT_EFFORT": "vaultagent"},
		}, "vaultagent"},
		{"empty when nothing set (no default)", Config{Agent: "claude"}, ""},
	}
	for _, tc := range cases {
		if got := tc.cfg.JobEffort("synthesize", ""); got != tc.want {
			t.Errorf("%s: JobEffort = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEnvWinsOverVaultViaLoad(t *testing.T) {
	clearModelEffortEnv(t)
	repo := writeVaultConfig(t, "jobs:\n  synthesize:\n    model: opus\n")
	// A deployment sets the env knob — it must win over the vault file.
	t.Setenv("KNOWLEDGE_SYNTHESIZE_MODEL", "sonnet")
	cfg, err := Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.JobModel("synthesize", ""); got != "sonnet" {
		t.Errorf("JobModel = %q, want sonnet (env wins over vault)", got)
	}
}

func TestVaultSchedule(t *testing.T) {
	clearModelEffortEnv(t)
	repo := writeVaultConfig(t, `jobs:
  compile:
    schedule: "@every 30m"
`)
	// The vault schedule flows through JobSchedule when no env var is set — and the raw override
	// field stays empty, so nothing gets baked into the daemon unit.
	cfg, err := Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompileSchedule != "" {
		t.Errorf("CompileSchedule override = %q, want empty (vault value must not populate the raw field)", cfg.CompileSchedule)
	}
	if got := cfg.JobSchedule("compile"); got != "@every 30m" {
		t.Errorf("JobSchedule(compile) = %q, want @every 30m (from vault)", got)
	}
	// A job the vault file omits falls back to the built-in default.
	if got := cfg.JobSchedule("synthesize"); got != DefaultSynthesizeSchedule {
		t.Errorf("JobSchedule(synthesize) = %q, want default %q", got, DefaultSynthesizeSchedule)
	}

	// A deployment's env var still wins over the vault schedule (and populates the override field,
	// so it IS persisted into the unit).
	t.Setenv("KNOWLEDGE_COMPILE_SCHEDULE", "@daily")
	cfg, err = Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompileSchedule != "@daily" {
		t.Errorf("CompileSchedule override = %q, want @daily (env populates the override field)", cfg.CompileSchedule)
	}
	if got := cfg.JobSchedule("compile"); got != "@daily" {
		t.Errorf("JobSchedule(compile) = %q, want @daily (env wins over vault)", got)
	}
}

func TestLegacyConfigEnvWarns(t *testing.T) {
	// A leftover legacy config.env (no longer read) must not affect config and must be a clean no-op
	// for resolution — the migration warning is best-effort and not asserted here.
	repo := t.TempDir()
	dir := filepath.Join(repo, ".knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.env"), []byte("KNOWLEDGE_COMPILE_SCHEDULE=@yearly\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clearModelEffortEnv(t)
	cfg, err := Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.JobSchedule("compile"); got != DefaultCompileSchedule {
		t.Errorf("JobSchedule(compile) = %q, want default (legacy config.env must be ignored)", got)
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
