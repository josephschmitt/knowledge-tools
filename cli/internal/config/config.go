// Package config resolves the KNOWLEDGE_* configuration the vault jobs and daemon run on.
//
// It ports scripts/load-env.sh (a repo-root .env whose values are overridden by the real
// environment) and the KNOWLEDGE_* knobs that vault-lib.sh, vault-compile.sh, and vault-job.sh
// read. The one deliberate change from the bash scripts: the schedule knobs move from systemd
// OnCalendar expressions to cron expressions (robfig/cron grammar), because the daemon owns
// scheduling internally now — see Config.CompileSchedule and friends.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults mirror the bash scripts' defaults so behavior is unchanged across the port.
const (
	DefaultInstance        = "default"
	DefaultAgent           = "claude" // KNOWLEDGE_AGENT
	DefaultCompileCooldown = 3600     // seconds; KNOWLEDGE_COMPILE_COOLDOWN

	// Schedules are cron expressions (robfig/cron). These mirror the old OnCalendar defaults:
	//   compile    hourly
	//   synthesize Sun 04:30 America/Detroit (weekly, off-peak)
	//   resolve    daily 03:30 America/Detroit (staggered 1h before synthesize)
	DefaultCompileSchedule    = "@hourly"
	DefaultSynthesizeSchedule = "CRON_TZ=America/Detroit 30 4 * * 0"
	DefaultResolveSchedule    = "CRON_TZ=America/Detroit 30 3 * * *"
)

// instanceRe is the slug allowed for KNOWLEDGE_INSTANCE — same constraint install.sh enforced,
// so the value is safe to embed in unit names, file paths, and JSON without escaping.
var instanceRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Config is the resolved configuration for one vault instance.
type Config struct {
	// Repo is KNOWLEDGE_REPO — the absolute path to the vault repo. Required for the jobs and
	// the daemon; not required for uninstall (which touches nothing inside the vault).
	Repo string
	// Instance is KNOWLEDGE_INSTANCE (default "default"). Keys the lock, schedules, and units.
	Instance string
	// Agent is KNOWLEDGE_AGENT — which headless harness runs the jobs: claude (default) | codex |
	// opencode | custom.
	Agent string
	// AgentBin is KNOWLEDGE_AGENT_BIN — the harness binary path. Empty lets each driver pick its
	// own default (claude → ~/.local/bin/claude, codex → "codex", opencode → "opencode"). The
	// deprecated CLAUDE_BIN is honored as a fallback.
	AgentBin string
	// AgentCmd is KNOWLEDGE_AGENT_CMD — the command template, required only when Agent == "custom".
	AgentCmd string
	// AgentModel is KNOWLEDGE_AGENT_MODEL — the fallback model for all jobs (per-job knobs win).
	// Empty lets the harness use its own configured default; the claude agent falls back to opus.
	AgentModel string
	// AgentEffort is KNOWLEDGE_AGENT_EFFORT — the fallback reasoning effort (per-job knobs win).
	// Only harnesses with an effort knob (claude has none; codex does) honor it.
	AgentEffort string
	// Per-job model/effort overrides (KNOWLEDGE_{COMPILE,SYNTHESIZE,RESOLVE}_{MODEL,EFFORT}).
	CompileModel, SynthesizeModel, ResolveModel    string
	CompileEffort, SynthesizeEffort, ResolveEffort string
	// vault holds the allowlisted model/effort/schedule knobs from <repo>/.knowledge/config.yaml
	// (see loadVaultConfig), flattened to KNOWLEDGE_* keys — a git-versioned default tier that
	// travels with the vault. It sits a tier BELOW the env knobs in JobModel/JobEffort and in the
	// schedule resolution in Load: a value here applies only when the whole env layer is empty for
	// that dimension, so any deployment overrides it without editing vault content. Kept as its own
	// map (not seeded into os.Env) precisely so the whole env layer wins over the whole vault layer.
	// nil/empty when the file is absent.
	vault map[string]string
	// CompileCooldown is KNOWLEDGE_COMPILE_COOLDOWN seconds between allowed manual compiles.
	CompileCooldown int
	// ReviewChannel is KNOWLEDGE_REVIEW_CHANNEL ("github" | "files" | "" for auto-detect).
	ReviewChannel string
	// GithubRepo is KNOWLEDGE_GITHUB_REPO (owner/name) for the github review channel.
	GithubRepo string
	// VaultLock is KNOWLEDGE_VAULT_LOCK — the per-instance lock path.
	VaultLock string
	// Cron schedules (robfig/cron grammar). See the Default*Schedule constants.
	CompileSchedule    string
	SynthesizeSchedule string
	ResolveSchedule    string
	// SiteRebuildURL is KNOWLEDGE_SITE_REBUILD_URL — if set, CommitAndPush POSTs to it after a
	// commit lands, telling the knowledge-site container to rebuild. Empty disables the trigger.
	SiteRebuildURL string
	// SiteRebuildToken is KNOWLEDGE_SITE_REBUILD_TOKEN — the bearer secret sent on that POST
	// (must match the container's same-named value).
	SiteRebuildToken string
}

// LoadDotenv reads a KEY=value .env file and exports any key NOT already set in the environment,
// so the real environment always wins (matching scripts/load-env.sh). Blank lines and # comments
// are ignored; one layer of matching surrounding quotes is stripped from values; values are
// literal (no shell expansion). A missing file is not an error.
//
// path defaults to $KNOWLEDGE_ENV_FILE, else ".env" in the current directory.
func LoadDotenv(path string) error {
	if path == "" {
		path = os.Getenv("KNOWLEDGE_ENV_FILE")
	}
	if path == "" {
		path = ".env"
	}
	kv, err := parseEnvFile(path)
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, ok := os.LookupEnv(k); !ok {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseEnvFile parses a KEY=value file into a map. Blank lines and # comments are ignored; one
// layer of matching surrounding quotes is stripped from values; values are literal (no shell
// expansion). A missing file returns (nil, nil) — the caller decides whether that's an error.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		// Strip one layer of matching surrounding quotes (after trimming, so `KEY = "v"` works).
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		kv[key] = val
	}
	return kv, sc.Err()
}

// ValidateInstance enforces the [A-Za-z0-9_-] slug install.sh/uninstall.sh require.
func ValidateInstance(instance string) error {
	if !instanceRe.MatchString(instance) {
		return fmt.Errorf("KNOWLEDGE_INSTANCE=%q must be a non-empty slug of [A-Za-z0-9_-]", instance)
	}
	return nil
}

// Load resolves the configuration from the environment for the given instance and repo. Pass the
// instance/repo already resolved by the CLI (flag-over-env); empty strings fall back to the env
// var then the default. The remaining KNOWLEDGE_* knobs are read straight from the environment
// (after LoadDotenv has seeded any .env values).
func Load(instance, repo string) (*Config, error) {
	if instance == "" {
		instance = os.Getenv("KNOWLEDGE_INSTANCE")
	}
	if instance == "" {
		instance = DefaultInstance
	}
	if err := ValidateInstance(instance); err != nil {
		return nil, err
	}

	if repo == "" {
		repo = os.Getenv("KNOWLEDGE_REPO")
	}

	// Vault-layer knobs from <repo>/.knowledge/config.yaml — a git-versioned default tier below the
	// env knobs (see JobModel/JobEffort, the schedule fields below, and the vault field). Non-fatal
	// on error so a malformed vault file can't wedge the daemon; nil when repo is unset (uninstall).
	vault := loadVaultConfig(repo)

	cfg := &Config{
		Repo:               repo,
		vault:              vault,
		Instance:           instance,
		Agent:              envOr("KNOWLEDGE_AGENT", DefaultAgent),
		AgentBin:           agentBin(),
		AgentCmd:           os.Getenv("KNOWLEDGE_AGENT_CMD"),
		AgentModel:         os.Getenv("KNOWLEDGE_AGENT_MODEL"),
		AgentEffort:        os.Getenv("KNOWLEDGE_AGENT_EFFORT"),
		CompileModel:       os.Getenv("KNOWLEDGE_COMPILE_MODEL"),
		SynthesizeModel:    os.Getenv("KNOWLEDGE_SYNTHESIZE_MODEL"),
		ResolveModel:       os.Getenv("KNOWLEDGE_RESOLVE_MODEL"),
		CompileEffort:      os.Getenv("KNOWLEDGE_COMPILE_EFFORT"),
		SynthesizeEffort:   os.Getenv("KNOWLEDGE_SYNTHESIZE_EFFORT"),
		ResolveEffort:      os.Getenv("KNOWLEDGE_RESOLVE_EFFORT"),
		CompileCooldown:    envInt("KNOWLEDGE_COMPILE_COOLDOWN", DefaultCompileCooldown),
		ReviewChannel:      os.Getenv("KNOWLEDGE_REVIEW_CHANNEL"),
		GithubRepo:         os.Getenv("KNOWLEDGE_GITHUB_REPO"),
		VaultLock:          envOr("KNOWLEDGE_VAULT_LOCK", defaultVaultLock(instance)),
		// Schedules resolve flag > env > vault > default (the flag layer is applied by InstallCmd
		// after Load); env still wins over the vault tier, mirroring JobModel/JobEffort.
		CompileSchedule:    firstNonEmpty(os.Getenv("KNOWLEDGE_COMPILE_SCHEDULE"), vault["KNOWLEDGE_COMPILE_SCHEDULE"], DefaultCompileSchedule),
		SynthesizeSchedule: firstNonEmpty(os.Getenv("KNOWLEDGE_SYNTHESIZE_SCHEDULE"), vault["KNOWLEDGE_SYNTHESIZE_SCHEDULE"], DefaultSynthesizeSchedule),
		ResolveSchedule:    firstNonEmpty(os.Getenv("KNOWLEDGE_RESOLVE_SCHEDULE"), vault["KNOWLEDGE_RESOLVE_SCHEDULE"], DefaultResolveSchedule),
		SiteRebuildURL:     os.Getenv("KNOWLEDGE_SITE_REBUILD_URL"),
		SiteRebuildToken:   os.Getenv("KNOWLEDGE_SITE_REBUILD_TOKEN"),
	}

	return cfg, nil
}

// vaultConfigFile is the shape of <repo>/.knowledge/config.yaml. The struct IS the allowlist: only
// the fields declared here are representable, so committed vault content can never set KNOWLEDGE_REPO,
// tokens, or git/site/auth wiring — any other YAML key decodes into nothing and is dropped. Only the
// three known job names (compile/synthesize/resolve) are honored when flattening Jobs.
type vaultConfigFile struct {
	// Defaults are the agent-wide model/effort fallbacks (per-job values under Jobs win over these).
	Defaults struct {
		Model  string `yaml:"model"`
		Effort string `yaml:"effort"`
	} `yaml:"defaults"`
	// Jobs holds per-job schedule/model/effort, keyed by job name (compile|synthesize|resolve).
	Jobs map[string]struct {
		Schedule string `yaml:"schedule"`
		Model    string `yaml:"model"`
		Effort   string `yaml:"effort"`
	} `yaml:"jobs"`
}

// vaultJobs is the set of job names honored in the vault config's `jobs:` map; any other key is
// ignored so a stray `jobs.evil.schedule` can't smuggle in a KNOWLEDGE_* value.
var vaultJobs = []string{"compile", "synthesize", "resolve"}

// loadVaultConfig reads <repo>/.knowledge/config.yaml and flattens its allowlisted fields into a
// map keyed by the KNOWLEDGE_* names JobModel/JobEffort and the Load schedule tier read. Returns nil
// when repo is empty or the file can't be parsed (the latter logged to stderr and treated as
// absent); an absent file yields an empty map. Either way a missing or malformed file is a clean
// no-op — c.vault reads then return "" for every key. As a migration aid, a leftover legacy
// .knowledge/config.env (the old KEY=value format, no longer read) is flagged on stderr.
func loadVaultConfig(repo string) map[string]string {
	if repo == "" {
		return nil
	}
	dir := filepath.Join(repo, ".knowledge")
	if _, err := os.Stat(filepath.Join(dir, "config.env")); err == nil {
		fmt.Fprintf(os.Stderr, "knowledge-tools: warning: .knowledge/config.env is no longer read; "+
			"migrate its settings to .knowledge/config.yaml\n")
	}

	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "knowledge-tools: warning: ignoring .knowledge/config.yaml: %v\n", err)
		}
		return nil
	}
	var vf vaultConfigFile
	if err := yaml.Unmarshal(b, &vf); err != nil {
		fmt.Fprintf(os.Stderr, "knowledge-tools: warning: ignoring .knowledge/config.yaml: %v\n", err)
		return nil
	}

	out := map[string]string{}
	setNonEmpty := func(key, val string) {
		if val != "" {
			out[key] = val
		}
	}
	setNonEmpty("KNOWLEDGE_AGENT_MODEL", vf.Defaults.Model)
	setNonEmpty("KNOWLEDGE_AGENT_EFFORT", vf.Defaults.Effort)
	for _, job := range vaultJobs {
		j := vf.Jobs[job]
		up := strings.ToUpper(job)
		setNonEmpty("KNOWLEDGE_"+up+"_SCHEDULE", j.Schedule)
		setNonEmpty("KNOWLEDGE_"+up+"_MODEL", j.Model)
		setNonEmpty("KNOWLEDGE_"+up+"_EFFORT", j.Effort)
	}
	return out
}

// JobModel resolves the model for a job in tiers: a caller-supplied override wins (a per-invocation
// value from the CLI flag / MCP tool / REST body), then the env layer (per-job, then
// KNOWLEDGE_AGENT_MODEL), then the vault layer (per-job, then agent-wide from .knowledge/config.env),
// then the agent's default. The whole env layer wins over the whole vault layer, so a deployment's
// KNOWLEDGE_AGENT_MODEL still overrides a vault-declared per-job value. Only the claude agent has a
// real default (opus) — preserving the model the old slash-command frontmatter declared; other
// harnesses default empty (use their own model). override is empty when the caller didn't specify one.
func (c *Config) JobModel(job, override string) string {
	var perJobEnv, vaultKey string
	switch job {
	case "compile":
		perJobEnv, vaultKey = c.CompileModel, "KNOWLEDGE_COMPILE_MODEL"
	case "synthesize":
		perJobEnv, vaultKey = c.SynthesizeModel, "KNOWLEDGE_SYNTHESIZE_MODEL"
	case "resolve":
		perJobEnv, vaultKey = c.ResolveModel, "KNOWLEDGE_RESOLVE_MODEL"
	}
	// Flat order encodes the tiers: caller override, then env per-job/agent, then vault per-job/agent.
	if m := firstNonEmpty(override, perJobEnv, c.AgentModel, c.vault[vaultKey], c.vault["KNOWLEDGE_AGENT_MODEL"]); m != "" {
		return m
	}
	if c.Agent == "" || c.Agent == "claude" {
		return "opus"
	}
	return ""
}

// JobEffort resolves the reasoning effort for a job in tiers: a caller-supplied override wins (a
// per-invocation value from the CLI flag / MCP tool / REST body), then the env layer (per-job, then
// KNOWLEDGE_AGENT_EFFORT), then the vault layer (per-job, then agent-wide from .knowledge/config.env).
// No agent-specific default — effort values are harness-specific and passed through verbatim (no
// translation): claude honors --effort (low|medium|high|xhigh|max) and codex model_reasoning_effort
// (low|medium|high); opencode has no knob and drops it; custom uses {{effort}} if its template
// references it. Empty means unset. override is empty when the caller didn't specify one.
func (c *Config) JobEffort(job, override string) string {
	var perJobEnv, vaultKey string
	switch job {
	case "compile":
		perJobEnv, vaultKey = c.CompileEffort, "KNOWLEDGE_COMPILE_EFFORT"
	case "synthesize":
		perJobEnv, vaultKey = c.SynthesizeEffort, "KNOWLEDGE_SYNTHESIZE_EFFORT"
	case "resolve":
		perJobEnv, vaultKey = c.ResolveEffort, "KNOWLEDGE_RESOLVE_EFFORT"
	}
	// Flat order encodes the tiers: caller override, then env per-job/agent, then vault per-job/agent.
	return firstNonEmpty(override, perJobEnv, c.AgentEffort, c.vault[vaultKey], c.vault["KNOWLEDGE_AGENT_EFFORT"])
}

// agentBin resolves KNOWLEDGE_AGENT_BIN, honoring the deprecated CLAUDE_BIN as a fallback. Empty
// when neither is set, so each driver supplies its own default binary.
func agentBin() string {
	if v := os.Getenv("KNOWLEDGE_AGENT_BIN"); v != "" {
		return v
	}
	if v := os.Getenv("CLAUDE_BIN"); v != "" {
		fmt.Fprintln(os.Stderr, "knowledge-tools: CLAUDE_BIN is deprecated; set KNOWLEDGE_AGENT_BIN instead.")
		return v
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// RequireRepo returns an error if KNOWLEDGE_REPO is unset — the jobs and daemon need it.
func (c *Config) RequireRepo() error {
	if c.Repo == "" {
		return fmt.Errorf("set KNOWLEDGE_REPO to the vault path (in .env, the environment, or --vault)")
	}
	return nil
}

// CompileDir is <repo>/inbox/.compile — the coordination dir shared with the MCP service.
func (c *Config) CompileDir() string { return filepath.Join(c.Repo, "inbox", ".compile") }

// defaultVaultLock mirrors vault-lib.sh: ~/.local/state/knowledge-tools/vault-<instance>.lock.
func defaultVaultLock(instance string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "knowledge-tools", "vault-"+instance+".lock")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}
