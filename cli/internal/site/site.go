// Package site builds the static Quartz site the vault service serves at / and publishes it
// outside the vault. It ports scripts/vault-site.sh into the CLI so the daemon can keep the site
// fresh after each compile under the *same* flock the jobs use (the bash script used a separate
// lock implementation that didn't mutually exclude with the Go daemon on macOS).
//
// Quartz is a clone-and-customize generator (NOT an npm dependency): the script maintains a pinned
// upstream git checkout, overlays this repo's config (embedded below, since the binary is
// standalone), stages a privacy-safe copy of the vault content (ONLY index.md + library/), runs
// `npx quartz build`, and atomically publishes the output. Read-only w.r.t. the vault.
package site

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
)

// The two config files overlaid onto the Quartz checkout. Embedded (committed copies of the
// repo-root site/*.ts); keep them in sync via `make sync-site` (CI guards drift).
//
//go:embed quartz/quartz.config.ts quartz/quartz.layout.ts
var quartzConfigFS embed.FS

// minNodeMajor is Quartz v4's Node floor.
const minNodeMajor = 20

// Options controls a build.
type Options struct {
	// NoLock skips acquiring the shared lock (the caller already holds it).
	NoLock bool
	// Soft makes a build failure a no-op (exit success, leave the published site in place) — for
	// the daemon's post-compile build, where a Quartz hiccup must not fail the content job.
	Soft bool
}

// Build builds and publishes the site for the configured vault instance. Returns vault.ErrLocked
// if another job holds the lock (and NoLock is false) — treat as a clean no-op.
func Build(ctx context.Context, cfg *config.Config, opts Options) error {
	if err := cfg.RequireRepo(); err != nil {
		return err
	}

	log.Printf("site: building for instance %q (vault: %s, ref: %s)", cfg.Instance, cfg.Repo, cfg.QuartzRef)

	// Quartz v4 needs Node >= 20.
	if err := checkNode(); err != nil {
		return maybeSoft(opts, err)
	}

	if !opts.NoLock {
		lock, err := vault.AcquireLock(cfg.VaultLock)
		if err != nil {
			if err == vault.ErrLocked {
				log.Printf("site: another vault job holds the lock — skipping.")
				return vault.ErrLocked
			}
			return err
		}
		defer func() { _ = lock.Release() }()
	}

	if err := ensureQuartz(ctx, cfg); err != nil {
		return maybeSoft(opts, err)
	}
	if err := overlayConfig(cfg); err != nil {
		return maybeSoft(opts, err)
	}
	if err := stageContent(cfg); err != nil {
		return maybeSoft(opts, err)
	}
	if err := buildAndPublish(ctx, cfg); err != nil {
		return maybeSoft(opts, err)
	}

	log.Printf("site: published to %s", cfg.SiteRoot)
	return nil
}

// maybeSoft turns a real error into a logged no-op when Soft is set (the bash --soft).
func maybeSoft(opts Options, err error) error {
	if err == nil {
		return nil
	}
	if opts.Soft {
		log.Printf("site: ERROR: %v", err)
		log.Printf("site: (--soft) leaving the previously published site in place.")
		return nil
	}
	return err
}

func checkNode() error {
	out, err := exec.Command("node", "-v").Output()
	if err != nil {
		return fmt.Errorf("node not found on PATH — Quartz needs Node >= %d", minNodeMajor)
	}
	// out like "v20.11.0"
	v := string(out)
	major := 0
	if len(v) > 1 && v[0] == 'v' {
		_, _ = fmt.Sscanf(v[1:], "%d", &major)
	}
	if major < minNodeMajor {
		return fmt.Errorf("node %s is too old — Quartz needs Node >= %d", string(out), minNodeMajor)
	}
	return nil
}

// ensureQuartz maintains the pinned Quartz checkout and installs deps when the ref changes.
func ensureQuartz(ctx context.Context, cfg *config.Config) error {
	dir := cfg.QuartzDir
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		log.Printf("site: cloning Quartz %s into %s", cfg.QuartzRef, dir)
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := runCmd(ctx, "", "git", "clone", "--quiet", "--depth", "1", "--branch", cfg.QuartzRef, cfg.QuartzURL, dir); err != nil {
			return fmt.Errorf("git clone of Quartz %s failed: %w", cfg.QuartzRef, err)
		}
	} else {
		log.Printf("site: updating Quartz checkout to %s", cfg.QuartzRef)
		// Fetch the pinned tag (fall back to a ref fetch), then force-checkout it.
		if err := runCmd(ctx, dir, "git", "fetch", "--quiet", "--depth", "1", "origin", "refs/tags/"+cfg.QuartzRef+":refs/tags/"+cfg.QuartzRef); err != nil {
			if err := runCmd(ctx, dir, "git", "fetch", "--quiet", "--depth", "1", "origin", cfg.QuartzRef); err != nil {
				return fmt.Errorf("git fetch of Quartz %s failed: %w", cfg.QuartzRef, err)
			}
		}
		if err := runCmd(ctx, dir, "git", "checkout", "--quiet", "--force", cfg.QuartzRef); err != nil {
			return fmt.Errorf("git checkout of Quartz %s failed: %w", cfg.QuartzRef, err)
		}
	}

	// Install deps only when the checked-out ref changes (or node_modules is missing) — npm ci is slow.
	stampFile := filepath.Join(dir, ".knowledge-tools-ref")
	stamp, _ := os.ReadFile(stampFile)
	_, nmErr := os.Stat(filepath.Join(dir, "node_modules"))
	if nmErr != nil || string(stamp) != cfg.QuartzRef {
		log.Printf("site: installing Quartz dependencies (npm ci) — this can take a while on first run")
		if err := runCmd(ctx, dir, "npm", "ci"); err != nil {
			return fmt.Errorf("npm ci in %s failed: %w", dir, err)
		}
		if err := os.WriteFile(stampFile, []byte(cfg.QuartzRef), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// overlayConfig writes the embedded quartz.config.ts + quartz.layout.ts into the checkout.
func overlayConfig(cfg *config.Config) error {
	for _, f := range []string{"quartz.config.ts", "quartz.layout.ts"} {
		data, err := quartzConfigFS.ReadFile("quartz/" + f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.QuartzDir, f), data, 0o644); err != nil {
			return fmt.Errorf("could not overlay %s: %w", f, err)
		}
	}
	return nil
}

// stageContent copies the privacy allowlist (ONLY index.md + library/) into a clean staging dir.
// Rebuilding the stage from scratch each run gives the bash rsync --delete semantics (notes
// removed from the vault stop being published).
func stageContent(cfg *config.Config) error {
	if err := os.RemoveAll(cfg.SiteStage); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.SiteStage, 0o755); err != nil {
		return err
	}
	staged := 0
	if src := filepath.Join(cfg.Repo, "index.md"); fileExists(src) {
		if err := copyFile(src, filepath.Join(cfg.SiteStage, "index.md")); err != nil {
			return err
		}
		staged++
	}
	if src := filepath.Join(cfg.Repo, "library"); dirExists(src) {
		n, err := copyTree(src, filepath.Join(cfg.SiteStage, "library"))
		if err != nil {
			return err
		}
		staged += n
	}
	if staged == 0 {
		return fmt.Errorf("no content to publish — neither %s/index.md nor %s/library exists", cfg.Repo, cfg.Repo)
	}
	log.Printf("site: staged content from index.md + library/")
	return nil
}

// buildAndPublish runs `npx quartz build` into a temp output, then atomically swaps it into place
// so the service never serves a half-built tree. The npx subprocess inherits the environment, so
// KNOWLEDGE_SITE_TITLE / KNOWLEDGE_SITE_BASE_URL reach quartz.config.ts.
func buildAndPublish(ctx context.Context, cfg *config.Config) error {
	tmp := cfg.SiteRoot + ".tmp"
	prev := cfg.SiteRoot + ".prev"
	_ = os.RemoveAll(tmp)

	log.Printf("site: running quartz build")
	if err := runCmd(ctx, cfg.QuartzDir, "npx", "quartz", "build", "-d", cfg.SiteStage, "-o", tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("quartz build failed: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SiteRoot), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(prev)
	if _, err := os.Stat(cfg.SiteRoot); err == nil {
		if err := os.Rename(cfg.SiteRoot, prev); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, cfg.SiteRoot); err != nil {
		return err
	}
	_ = os.RemoveAll(prev)
	return nil
}

// runCmd runs a command in dir (empty = current) with stdout/stderr inherited so output lands in
// the daemon unit's log (or the terminal for a manual run).
func runCmd(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- small fs helpers ---

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// copyTree recursively copies src into dst, returning the number of markdown files copied.
func copyTree(src, dst string) (int, error) {
	md := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.Ext(p) == ".md" {
			md++
		}
		return copyFile(p, target)
	})
	return md, err
}
