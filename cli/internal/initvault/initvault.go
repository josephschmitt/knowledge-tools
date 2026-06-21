// Package initvault seeds a fresh vault from the embedded template/ — a one-shot scaffold, not a
// sync. Ports scripts/init-vault.sh: strictly copy-if-absent (a tuned CLAUDE.md or command is
// never overwritten — post-seed drift is the design), no --force, and it leaves git alone.
//
// The template lives at the repo root but the binary is standalone, so a committed copy is embedded
// here. Keep it in sync with the repo-root template/ via `make sync-template` (CI guards drift).
package initvault

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// all: includes dotfiles (.gitkeep, .gitignore, .claude/), which a bare //go:embed would skip.
//
//go:embed all:template
var templateFS embed.FS

// Result reports what Seed did.
type Result struct {
	Created int
	Skipped int
}

// Seed copies the template into vaultDir, copy-if-absent. Creates vaultDir if missing.
func Seed(vaultDir string) (Result, error) {
	var res Result
	if vaultDir == "" {
		return res, fmt.Errorf("no target — pass a vault dir or set KNOWLEDGE_REPO")
	}
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		return res, err
	}

	// Collect template files (relative to the embedded "template/" root), sorted for stable output.
	var files []string
	err := fs.WalkDir(templateFS, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	sort.Strings(files)

	for _, src := range files {
		rel := strings.TrimPrefix(src, "template/")
		dest := filepath.Join(vaultDir, rel)
		base := filepath.Base(rel)

		// A .gitkeep only carries an otherwise-empty dir. Don't plant one where real content
		// already exists (e.g. a grown wiki/) — the dir no longer needs keeping.
		if base == ".gitkeep" {
			if dirHasContent(filepath.Dir(dest)) {
				res.Skipped++
				continue
			}
		}

		if _, err := os.Stat(dest); err == nil {
			fmt.Printf("  skip   %s (exists)\n", rel)
			res.Skipped++
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, err
		}
		data, err := templateFS.ReadFile(src)
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return res, err
		}
		fmt.Printf("  create %s\n", rel)
		res.Created++
	}
	return res, nil
}

func dirHasContent(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
