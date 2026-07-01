package agent

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// build is a tiny helper that builds the argv for a driver+invocation and fails on error.
func build(t *testing.T, d Driver, inv Invocation) []string {
	t.Helper()
	cmd, cleanup, err := d.Build(context.Background(), inv)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	return cmd.Args[1:] // drop argv[0] (the binary path)
}

func TestClaudeDriverArgvParity(t *testing.T) {
	d := &claudeDriver{bin: "/usr/bin/claude"}

	// Compile: no grants, no model → identical flag structure to the legacy RunClaude argv.
	got := build(t, d, Invocation{Prompt: "/compile-inbox"})
	want := []string{"-p", "/compile-inbox", "--permission-mode", "acceptEdits"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("compile argv = %v, want %v", got, want)
	}

	// Github synthesize: neutral grants are re-wrapped into Bash(<prefix>:*), matching the old
	// hardcoded ghTools exactly.
	got = build(t, d, Invocation{
		Prompt:      "the synthesize prompt",
		ShellGrants: []string{"gh issue list", "gh issue view", "gh issue create", "gh search issues"},
	})
	want = []string{
		"-p", "the synthesize prompt", "--permission-mode", "acceptEdits",
		"--allowedTools",
		"Bash(gh issue list:*)", "Bash(gh issue view:*)", "Bash(gh issue create:*)", "Bash(gh search issues:*)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("synthesize argv = %v, want %v", got, want)
	}

	// Model is passed through as --model when set (what the slash frontmatter used to carry).
	got = build(t, d, Invocation{Prompt: "p", Model: "opus"})
	want = []string{"-p", "p", "--permission-mode", "acceptEdits", "--model", "opus"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("model argv = %v, want %v", got, want)
	}
}

func TestClaudeBinDefault(t *testing.T) {
	cmd, _, err := (&claudeDriver{}).Build(context.Background(), Invocation{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// Compare with the OS-native separator (backslashes on Windows).
	want := filepath.Join(".local", "bin", "claude")
	if !strings.HasSuffix(cmd.Args[0], want) {
		t.Errorf("default claude bin = %q, want suffix %q", cmd.Args[0], want)
	}
}

func TestCodexDriver(t *testing.T) {
	d := &codexDriver{bin: "codex"}
	if d.SupportsShellGrants() {
		t.Error("codex must report no shell-grant support (all-or-nothing sandbox)")
	}
	got := build(t, d, Invocation{Prompt: "do it", Model: "gpt-5", Effort: "high"})
	want := []string{"exec", "do it", "--full-auto", "-m", "gpt-5", "-c", "model_reasoning_effort=high"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex argv = %v, want %v", got, want)
	}
	// Grants are ignored (no flag) — they never reach the argv.
	got = build(t, d, Invocation{Prompt: "p", ShellGrants: []string{"gh issue list"}})
	want = []string{"exec", "p", "--full-auto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex argv (grants ignored) = %v, want %v", got, want)
	}
}

func TestOpencodeDriver(t *testing.T) {
	d := &opencodeDriver{bin: "opencode"}
	if !d.SupportsShellGrants() {
		t.Error("opencode should support scoped shell grants via its config")
	}
	cmd, cleanup, err := d.Build(context.Background(), Invocation{Prompt: "p", Model: "anthropic/claude", ShellGrants: []string{"gh issue list"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	want := []string{"run", "p", "-m", "anthropic/claude"}
	if !reflect.DeepEqual(cmd.Args[1:], want) {
		t.Errorf("opencode argv = %v, want %v", cmd.Args[1:], want)
	}
	// The ephemeral config is pointed at via OPENCODE_CONFIG.
	var cfgPath string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "OPENCODE_CONFIG=") {
			cfgPath = strings.TrimPrefix(e, "OPENCODE_CONFIG=")
		}
	}
	if cfgPath == "" {
		t.Fatal("OPENCODE_CONFIG not set in cmd.Env")
	}
}

func TestCustomDriverTokenizer(t *testing.T) {
	d, err := newCustomDriver("mytool", "{{bin}} run --model {{model}} --prompt {{prompt}}")
	if err != nil {
		t.Fatal(err)
	}
	// Prompt with spaces stays a single argv element; empty model drops the --model pair.
	got := build(t, d, Invocation{Prompt: "a long prompt", Model: ""})
	want := []string{"run", "--prompt", "a long prompt"} // build() drops argv[0] ("mytool")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("custom argv = %v, want %v", got, want)
	}
	// With a model, the pair stays.
	got = build(t, d, Invocation{Prompt: "p", Model: "m"})
	want = []string{"run", "--model", "m", "--prompt", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("custom argv (model) = %v, want %v", got, want)
	}
}

func TestCustomDriverGrantsAndStdin(t *testing.T) {
	// {{grants}} present → supports grants and expands to multiple argv elements.
	d, err := newCustomDriver("", "agent --allow {{grants}} {{prompt_stdin}}")
	if err != nil {
		t.Fatal(err)
	}
	if !d.SupportsShellGrants() {
		t.Error("template with {{grants}} should support shell grants")
	}
	cmd, _, err := d.Build(context.Background(), Invocation{Prompt: "body", ShellGrants: []string{"gh issue list", "gh issue view"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--allow", "gh issue list", "gh issue view"}
	if !reflect.DeepEqual(cmd.Args[1:], want) {
		t.Errorf("custom grants argv = %v, want %v", cmd.Args[1:], want)
	}
	if cmd.Stdin == nil {
		t.Error("{{prompt_stdin}} should set cmd.Stdin")
	}
}

func TestCustomDriverEmptyTemplate(t *testing.T) {
	if _, err := newCustomDriver("", "   "); err == nil {
		t.Error("empty KNOWLEDGE_AGENT_CMD should error")
	}
}

func TestNewDriverUnknown(t *testing.T) {
	if _, err := NewDriver(Spec{Agent: "bard"}); err == nil {
		t.Error("unknown agent should error")
	}
	if d, err := NewDriver(Spec{Agent: ""}); err != nil || d.Name() != "claude" {
		t.Errorf("empty agent should default to claude, got %v err %v", d, err)
	}
}
