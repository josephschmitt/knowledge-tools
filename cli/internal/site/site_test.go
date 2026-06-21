package site

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStageContentAllowlist(t *testing.T) {
	repo := t.TempDir()
	stage := filepath.Join(t.TempDir(), "stage")
	// Public content.
	write(t, filepath.Join(repo, "index.md"), "home")
	write(t, filepath.Join(repo, "wiki", "note.md"), "a note")
	write(t, filepath.Join(repo, "wiki", "sub", "deep.md"), "deep")
	// Private content that must NEVER be published.
	write(t, filepath.Join(repo, "inbox", "secret.md"), "raw capture")
	write(t, filepath.Join(repo, "outputs", "report.md"), "output")
	write(t, filepath.Join(repo, "log.md"), "log")

	cfg := &config.Config{Repo: repo, SiteStage: stage}
	if err := stageContent(cfg); err != nil {
		t.Fatalf("stageContent: %v", err)
	}

	mustExist := []string{"index.md", filepath.Join("wiki", "note.md"), filepath.Join("wiki", "sub", "deep.md")}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(stage, f)); err != nil {
			t.Errorf("expected %s in stage: %v", f, err)
		}
	}
	mustNotExist := []string{"inbox", "outputs", "log.md", filepath.Join("inbox", "secret.md")}
	for _, f := range mustNotExist {
		if _, err := os.Stat(filepath.Join(stage, f)); !os.IsNotExist(err) {
			t.Errorf("PRIVACY LEAK: %s should not be staged", f)
		}
	}
}

func TestStageContentPrunesRemovedNotes(t *testing.T) {
	repo := t.TempDir()
	stage := filepath.Join(t.TempDir(), "stage")
	write(t, filepath.Join(repo, "index.md"), "home")
	write(t, filepath.Join(repo, "wiki", "old.md"), "old")

	cfg := &config.Config{Repo: repo, SiteStage: stage}
	if err := stageContent(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "wiki", "old.md")); err != nil {
		t.Fatal("old.md should be staged on first run")
	}

	// Remove the note from the vault and re-stage; the staged copy must be pruned.
	if err := os.Remove(filepath.Join(repo, "wiki", "old.md")); err != nil {
		t.Fatal(err)
	}
	if err := stageContent(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "wiki", "old.md")); !os.IsNotExist(err) {
		t.Error("old.md should be pruned from the stage after removal from the vault")
	}
}

func TestStageContentNoContent(t *testing.T) {
	repo := t.TempDir() // no index.md, no wiki/
	cfg := &config.Config{Repo: repo, SiteStage: filepath.Join(t.TempDir(), "stage")}
	if err := stageContent(cfg); err == nil {
		t.Error("stageContent with no public content should error")
	}
}
