package jobs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/josephschmitt/knowledge-tools/cli/internal/agent"
)

func TestHasAnsweredLine(t *testing.T) {
	yes := [][]byte{
		[]byte("status: answered"),
		[]byte("title: x\nstatus: answered\nbody"),
		[]byte("a\nstatus: answered\n"),
	}
	no := [][]byte{
		[]byte("status: open"),
		[]byte("  status: answered"), // not anchored (leading space)
		[]byte("status: answered yes"),
		[]byte(""),
	}
	for _, b := range yes {
		if !hasAnsweredLine(b) {
			t.Errorf("hasAnsweredLine(%q) = false, want true", b)
		}
	}
	for _, b := range no {
		if hasAnsweredLine(b) {
			t.Errorf("hasAnsweredLine(%q) = true, want false", b)
		}
	}
}

func TestChannelConfig(t *testing.T) {
	// files channel: -files skill, no gh tools, commits inbox/.review too.
	skill, gh, paths := channelConfig(JobSynthesize, "files")
	if skill != "synthesize-files" {
		t.Errorf("files skill = %q", skill)
	}
	if gh != nil {
		t.Errorf("files gh tools = %v, want nil", gh)
	}
	if !contains(paths, "inbox/.review") {
		t.Errorf("files commit paths = %v, want inbox/.review", paths)
	}

	// github synthesize: producer gh grants (create), no comment/close. Grants are bare prefixes
	// now (the agent driver wraps them), not Bash(...:*) strings.
	_, gh, paths = channelConfig(JobSynthesize, "github")
	if !contains(gh, "gh issue create") {
		t.Errorf("synthesize gh grants missing create: %v", gh)
	}
	if contains(gh, "gh issue close") {
		t.Errorf("synthesize should not grant close: %v", gh)
	}
	if contains(paths, "inbox/.review") {
		t.Errorf("github commit paths should not include inbox/.review: %v", paths)
	}

	// github resolve: consumer gh grants (comment/edit/close), no create.
	skill, gh, _ = channelConfig(JobResolve, "github")
	if skill != "resolve" {
		t.Errorf("github resolve skill = %q", skill)
	}
	if !contains(gh, "gh issue close") {
		t.Errorf("resolve gh grants missing close: %v", gh)
	}
	if contains(gh, "gh issue create") {
		t.Errorf("resolve should not grant create: %v", gh)
	}
}

// fakeDriver is a minimal agent.Driver for exercising channel policy.
type fakeDriver struct {
	name   string
	grants bool
}

func (d fakeDriver) Name() string              { return d.name }
func (d fakeDriver) SupportsShellGrants() bool { return d.grants }
func (d fakeDriver) Build(ctx context.Context, inv agent.Invocation) (*exec.Cmd, func(), error) {
	return exec.Command("true"), nil, nil
}

func TestResolveAgentChannel(t *testing.T) {
	granting := fakeDriver{name: "claude", grants: true}
	grantless := fakeDriver{name: "codex", grants: false}

	// A grant-capable agent keeps the auto-detected github channel.
	if ch, down, err := resolveAgentChannel("github", "", granting); err != nil || ch != "github" || down {
		t.Errorf("granting/github = (%q,%v,%v), want (github,false,nil)", ch, down, err)
	}
	// A grant-less agent downgrades an auto-detected github to files.
	if ch, down, err := resolveAgentChannel("github", "", grantless); err != nil || ch != "files" || !down {
		t.Errorf("grantless/github = (%q,%v,%v), want (files,true,nil)", ch, down, err)
	}
	// Forcing github on a grant-less agent is an error, not a silent downgrade.
	if _, _, err := resolveAgentChannel("github", "github", grantless); err == nil {
		t.Error("forced github on a grant-less agent should error")
	}
	// The files channel is untouched for any agent.
	if ch, down, err := resolveAgentChannel("files", "", grantless); err != nil || ch != "files" || down {
		t.Errorf("grantless/files = (%q,%v,%v), want (files,false,nil)", ch, down, err)
	}
}

func TestSkillPrompt(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".agents", "skills", "synthesize")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: synthesize\ndescription: d\n---\n\nFocus on $ARGUMENTS then sweep.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Frontmatter stripped, $ARGUMENTS substituted (empty for scheduled runs); not legacy.
	got, legacy, err := skillPrompt(repo, "synthesize", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Focus on  then sweep."; got != want || legacy {
		t.Errorf("prompt = %q legacy=%v, want %q legacy=false", got, legacy, want)
	}
	// With an argument.
	got, _, _ = skillPrompt(repo, "synthesize", "redis")
	if want := "Focus on redis then sweep."; got != want {
		t.Errorf("prompt with arg = %q, want %q", got, want)
	}
	// Missing procedure errors.
	if _, _, err := skillPrompt(repo, "nope", ""); err == nil {
		t.Error("missing procedure should error")
	}
}

// TestSkillPromptLegacyFallback covers a vault seeded before the skills migration: only
// .claude/commands/<name>.md exists, and the CLI feeds its body (flagging it as legacy).
func TestSkillPromptLegacyFallback(t *testing.T) {
	repo := t.TempDir()
	cmds := filepath.Join(repo, ".claude", "commands")
	if err := os.MkdirAll(cmds, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyBody := "---\ndescription: d\nmodel: opus\n---\n\nProcess the inbox.\n"
	if err := os.WriteFile(filepath.Join(cmds, "compile-inbox.md"), []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}

	got, legacy, err := skillPrompt(repo, "compile-inbox", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Process the inbox." || !legacy {
		t.Errorf("legacy fallback = %q legacy=%v, want %q legacy=true", got, legacy, "Process the inbox.")
	}

	// When the new skill ALSO exists, it wins (and is not flagged legacy).
	dir := filepath.Join(repo, ".agents", "skills", "compile-inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: compile-inbox\ndescription: d\n---\n\nNew body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, legacy, _ = skillPrompt(repo, "compile-inbox", "")
	if got != "New body." || legacy {
		t.Errorf("skills layout should win: got %q legacy=%v, want %q legacy=false", got, legacy, "New body.")
	}
}

func TestStripFrontmatter(t *testing.T) {
	if got := stripFrontmatter("---\na: b\n---\nbody\n"); got != "body\n" {
		t.Errorf("stripped = %q, want %q", got, "body\n")
	}
	// No frontmatter → unchanged.
	if got := stripFrontmatter("just body"); got != "just body" {
		t.Errorf("no-frontmatter = %q", got)
	}
	// Unterminated frontmatter → unchanged.
	if got := stripFrontmatter("---\nno close\n"); got != "---\nno close\n" {
		t.Errorf("unterminated = %q", got)
	}
}

func TestNextRunISO(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	if got := nextRunISO("", now); got != nil {
		t.Errorf("empty schedule = %v, want nil", got)
	}
	if got := nextRunISO("not a cron", now); got != nil {
		t.Errorf("bad schedule = %v, want nil", got)
	}
	if got := nextRunISO("@hourly", now); got == nil {
		t.Error("@hourly should yield a next time")
	}
	// CRON_TZ prefix must parse.
	if got := nextRunISO("CRON_TZ=America/Detroit 30 4 * * 0", now); got == nil {
		t.Error("CRON_TZ schedule should parse")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
