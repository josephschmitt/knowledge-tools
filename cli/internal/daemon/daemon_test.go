package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
)

func TestReadRequestOverrides(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	tests := []struct {
		name string
		path string
		want jobs.Overrides
	}{
		{
			"full JSON payload",
			write("full", `{"requested_at":"2026-07-02T00:00:00Z","model":"sonnet","effort":"high"}`),
			jobs.Overrides{Model: "sonnet", Effort: "high"},
		},
		{
			"model only",
			write("model", `{"model":"opus"}`),
			jobs.Overrides{Model: "opus"},
		},
		{
			"empty JSON object",
			write("empty", `{}`),
			jobs.Overrides{},
		},
		{
			// An older service wrote a bare ISO timestamp — not valid JSON, degrades to no override.
			"legacy bare-timestamp body",
			write("legacy", "2026-07-02T00:00:00Z\n"),
			jobs.Overrides{},
		},
		{
			"missing file",
			filepath.Join(dir, "does-not-exist"),
			jobs.Overrides{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readRequestOverrides(tt.path); got != tt.want {
				t.Errorf("readRequestOverrides = %+v, want %+v", got, tt.want)
			}
		})
	}
}
