package jobs

import (
	"testing"
	"time"
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
	// files channel: -files slash, no gh tools, commits inbox/.review too.
	slash, gh, paths := channelConfig(JobSynthesize, "files")
	if slash != "/synthesize-files" {
		t.Errorf("files slash = %q", slash)
	}
	if gh != nil {
		t.Errorf("files gh tools = %v, want nil", gh)
	}
	if !contains(paths, "inbox/.review") {
		t.Errorf("files commit paths = %v, want inbox/.review", paths)
	}

	// github synthesize: producer gh tools (create), no comment/close.
	_, gh, paths = channelConfig(JobSynthesize, "github")
	if !contains(gh, "Bash(gh issue create:*)") {
		t.Errorf("synthesize gh tools missing create: %v", gh)
	}
	if contains(gh, "Bash(gh issue close:*)") {
		t.Errorf("synthesize should not grant close: %v", gh)
	}
	if contains(paths, "inbox/.review") {
		t.Errorf("github commit paths should not include inbox/.review: %v", paths)
	}

	// github resolve: consumer gh tools (comment/edit/close), no create.
	slash, gh, _ = channelConfig(JobResolve, "github")
	if slash != "/resolve" {
		t.Errorf("github resolve slash = %q", slash)
	}
	if !contains(gh, "Bash(gh issue close:*)") {
		t.Errorf("resolve gh tools missing close: %v", gh)
	}
	if contains(gh, "Bash(gh issue create:*)") {
		t.Errorf("resolve should not grant create: %v", gh)
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
