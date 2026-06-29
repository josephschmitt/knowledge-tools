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
)

// Defaults mirror the bash scripts' defaults so behavior is unchanged across the port.
const (
	DefaultInstance        = "default"
	DefaultCompileCooldown = 3600 // seconds; KNOWLEDGE_COMPILE_COOLDOWN

	// Schedules are cron expressions (robfig/cron). These mirror the old OnCalendar defaults:
	//   compile    hourly
	//   synthesize Sun 04:30 America/Detroit (weekly, off-peak)
	//   resolve    daily 03:30 America/Detroit (staggered 1h before synthesize)
	DefaultCompileSchedule    = "@hourly"
	DefaultSynthesizeSchedule = "CRON_TZ=America/Detroit 30 4 * * 0"
	DefaultResolveSchedule    = "CRON_TZ=America/Detroit 30 3 * * *"

	// Static site (Quartz). Quartz is a clone-and-customize generator, pinned to a git ref.
	DefaultQuartzRef = "v4.5.2"
	DefaultQuartzURL = "https://github.com/jackyzha0/quartz"
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
	// ClaudeBin is CLAUDE_BIN — the claude binary the jobs invoke (default ~/.local/bin/claude).
	ClaudeBin string
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

	// --- Static site (Quartz) — the optional render the service serves at /. ---
	// SiteEnable (KNOWLEDGE_SITE_ENABLE) makes the daemon rebuild the site after each compile.
	SiteEnable bool
	// QuartzRef/URL/Dir: the pinned, shared Quartz checkout.
	QuartzRef string
	QuartzURL string
	QuartzDir string
	// SiteStage is the per-instance staging dir (privacy allowlist); SiteRoot is the published
	// output the service bind-mounts.
	SiteStage string
	SiteRoot  string
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
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

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
		if _, ok := os.LookupEnv(key); !ok {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return sc.Err()
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

	cfg := &Config{
		Repo:               repo,
		Instance:           instance,
		ClaudeBin:          envOr("CLAUDE_BIN", defaultClaudeBin()),
		CompileCooldown:    envInt("KNOWLEDGE_COMPILE_COOLDOWN", DefaultCompileCooldown),
		ReviewChannel:      os.Getenv("KNOWLEDGE_REVIEW_CHANNEL"),
		GithubRepo:         os.Getenv("KNOWLEDGE_GITHUB_REPO"),
		VaultLock:          envOr("KNOWLEDGE_VAULT_LOCK", defaultVaultLock(instance)),
		CompileSchedule:    envOr("KNOWLEDGE_COMPILE_SCHEDULE", DefaultCompileSchedule),
		SynthesizeSchedule: envOr("KNOWLEDGE_SYNTHESIZE_SCHEDULE", DefaultSynthesizeSchedule),
		ResolveSchedule:    envOr("KNOWLEDGE_RESOLVE_SCHEDULE", DefaultResolveSchedule),

		SiteEnable: envBool("KNOWLEDGE_SITE_ENABLE"),
		QuartzRef:  envOr("KNOWLEDGE_QUARTZ_REF", DefaultQuartzRef),
		QuartzURL:  envOr("KNOWLEDGE_QUARTZ_URL", DefaultQuartzURL),
		QuartzDir:  envOr("KNOWLEDGE_QUARTZ_DIR", filepath.Join(stateDir(), "quartz")),
		SiteStage:  envOr("KNOWLEDGE_SITE_STAGE", filepath.Join(stateDir(), "site-stage", instance)),
		SiteRoot:   envOr("KNOWLEDGE_SITE_ROOT", filepath.Join(stateDir(), "site", instance)),
	}
	return cfg, nil
}

// stateDir is the runtime state root, ~/.local/state/knowledge-tools (or under XDG_STATE_HOME) —
// the same base the lock uses, holding the shared Quartz checkout and per-instance site output.
func stateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "knowledge-tools")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "knowledge-tools")
}

// RequireRepo returns an error if KNOWLEDGE_REPO is unset — the jobs and daemon need it.
func (c *Config) RequireRepo() error {
	if c.Repo == "" {
		return fmt.Errorf("set KNOWLEDGE_REPO to the vault repo path (in .env, the environment, or --repo)")
	}
	return nil
}

// CompileDir is <repo>/inbox/.compile — the coordination dir shared with the MCP service.
func (c *Config) CompileDir() string { return filepath.Join(c.Repo, "inbox", ".compile") }

func defaultClaudeBin() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "claude")
}

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

// envBool reports whether key is set to a truthy value (1/true/yes/on, case-insensitive).
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}
