package jobs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

// issueJobCfg builds a config for a files-channel synthesize/resolve run: no gh/origin needed, and
// resolveAgentChannel leaves "files" untouched so the skill is <job>-files with no grants.
func issueJobCfg(t *testing.T, repo, claude string) *config.Config {
	t.Helper()
	return &config.Config{
		Repo:               repo,
		Instance:           "test",
		AgentBin:           claude,
		ReviewChannel:      "files",
		VaultLock:          filepath.Join(t.TempDir(), "vault.lock"),
		CompileSchedule:    "@hourly",
		SynthesizeSchedule: "@daily",
		ResolveSchedule:    "@daily",
	}
}

func TestSynthesizeStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a shell script")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	mkSkill(t, repo, "synthesize-files")
	// The stub captures the live status file mid-run (proving running:true was written before the
	// agent ran), then writes a library note like a real synthesize would.
	claude := stubClaude(t, "cp inbox/.compile/status-synthesize.json ran.json && mkdir -p library && echo x > library/n.md")

	cfg := issueJobCfg(t, repo, claude)
	if err := RunIssueJob(context.Background(), cfg, JobSynthesize, Overrides{}); err != nil {
		t.Fatalf("RunIssueJob synthesize: %v", err)
	}

	// Mid-run snapshot: running:true with the gerund summary.
	var mid jobStatus
	readJSONFile(t, filepath.Join(repo, "ran.json"), &mid)
	if !mid.Running {
		t.Error("status-synthesize.json should be running:true while the agent runs")
	}
	if mid.Summary != "synthesizing" {
		t.Errorf("mid-run summary = %q, want %q", mid.Summary, "synthesizing")
	}

	// Final status: finished, successful.
	var final jobStatus
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "status-synthesize.json"), &final)
	if final.Running {
		t.Error("status-synthesize.json running should be false after the run")
	}
	if final.Summary != "synthesized" {
		t.Errorf("final summary = %q, want %q", final.Summary, "synthesized")
	}
	if final.LastRunAt == "" {
		t.Error("final last_run_at should be set after a run")
	}

	// schedules.json now shows synthesize last_run_at populated.
	var sched struct {
		Jobs map[string]struct {
			LastRunAt *string `json:"last_run_at"`
		} `json:"jobs"`
	}
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "schedules.json"), &sched)
	if sched.Jobs["synthesize"].LastRunAt == nil {
		t.Error("synthesize last_run_at should be set after a run")
	}
}

func TestResolveNoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a shell script")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	mkSkill(t, repo, "resolve-files")
	// Nothing answered under inbox/.review/ → resolve short-circuits and must not invoke the agent.
	claude := stubClaude(t, "echo should-not-run >&2; exit 1")

	cfg := issueJobCfg(t, repo, claude)
	if err := RunIssueJob(context.Background(), cfg, JobResolve, Overrides{}); err != nil {
		t.Fatalf("RunIssueJob resolve on empty review queue should succeed, got %v", err)
	}

	var st jobStatus
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "status-resolve.json"), &st)
	if st.Running {
		t.Error("status-resolve.json running should be false after a no-op resolve")
	}
	if st.Summary != "nothing to resolve" {
		t.Errorf("summary = %q, want %q", st.Summary, "nothing to resolve")
	}
}

func TestResolveStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a shell script")
	}
	repo := initGitRepo(t)
	mkInbox(t, repo)
	mkSkill(t, repo, "resolve-files")
	// Seed one answered judgment call so answeredCount > 0 and the agent actually runs.
	reviewDir := filepath.Join(repo, "inbox", ".review")
	must(t, os.MkdirAll(reviewDir, 0o755))
	must(t, os.WriteFile(filepath.Join(reviewDir, "q.md"), []byte("status: answered\n\n## Question\nx\n"), 0o644))
	claude := stubClaude(t, "echo applied > library/applied.md 2>/dev/null || (mkdir -p library && echo applied > library/applied.md)")

	cfg := issueJobCfg(t, repo, claude)
	if err := RunIssueJob(context.Background(), cfg, JobResolve, Overrides{}); err != nil {
		t.Fatalf("RunIssueJob resolve: %v", err)
	}

	var st jobStatus
	readJSONFile(t, filepath.Join(cfg.CompileDir(), "status-resolve.json"), &st)
	if st.Running {
		t.Error("status-resolve.json running should be false after the run")
	}
	if st.Summary != "resolved" {
		t.Errorf("summary = %q, want %q", st.Summary, "resolved")
	}
	if st.LastRunAt == "" {
		t.Error("last_run_at should be set after a run")
	}
}
