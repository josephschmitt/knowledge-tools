package initvault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedCreatesTemplate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	res, err := Seed(dir)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if res.Created == 0 {
		t.Fatal("expected files to be created")
	}
	// Spot-check key seed files exist.
	for _, f := range []string{
		"CLAUDE.md", "index.md", "log.md",
		filepath.Join(".agents", "skills", "compile-inbox", "SKILL.md"),
		filepath.Join(".agents", "skills", "synthesize", "SKILL.md"),
		filepath.Join("inbox", ".gitkeep"),
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to be seeded: %v", f, err)
		}
	}

	// .claude/skills is a symlink to ../.agents/skills so Claude Code finds the skills.
	link := filepath.Join(dir, ".claude", "skills")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf(".claude/skills not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error(".claude/skills should be a symlink")
	}
	// It must resolve to a real directory holding the skills.
	if _, err := os.Stat(filepath.Join(link, "compile-inbox", "SKILL.md")); err != nil {
		t.Errorf(".claude/skills symlink does not resolve to the skills: %v", err)
	}
}

func TestSeedCopyIfAbsent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	if _, err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	// Tune a file; a re-seed must NOT overwrite it.
	tuned := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(tuned, []byte("MY TUNED LIBRARIAN"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Seed(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 {
		t.Errorf("re-seed created %d files, want 0", res.Created)
	}
	got, _ := os.ReadFile(tuned)
	if string(got) != "MY TUNED LIBRARIAN" {
		t.Error("re-seed overwrote a tuned file")
	}
}

func TestSeedSkipsGitkeepInPopulatedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	if _, err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	// Simulate a grown library/: remove the seeded .gitkeep, add real content.
	_ = os.Remove(filepath.Join(dir, "library", ".gitkeep"))
	if err := os.WriteFile(filepath.Join(dir, "library", "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	// The .gitkeep must NOT be re-planted into the now-populated library/.
	if _, err := os.Stat(filepath.Join(dir, "library", ".gitkeep")); !os.IsNotExist(err) {
		t.Error(".gitkeep should not be re-planted in a populated dir")
	}
}
