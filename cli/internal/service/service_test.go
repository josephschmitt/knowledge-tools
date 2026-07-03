package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
)

func testCfg() *config.Config {
	return &config.Config{
		Repo:               "/home/me/vault",
		Instance:           "work",
		AgentBin:           "/home/me/.local/bin/claude",
		CompileCooldown:    1800,
		ReviewChannel:      "files",
		GithubRepo:         "me/vault",
		CompileSchedule:    "@hourly",
		SynthesizeSchedule: "CRON_TZ=America/Detroit 30 4 * * 0",
		ResolveSchedule:    "30 3 * * *",
		SiteRebuildURL:     "http://knowledge-site:8080/rebuild",
		SiteRebuildToken:   "s3cret",
	}
}

func TestDaemonServiceContents(t *testing.T) {
	out := daemonServiceContents("/usr/bin/knowledge-tools")
	for _, want := range []string{
		"ExecStart=/usr/bin/knowledge-tools daemon",
		"Type=simple",
		"Restart=on-failure",
		"EnvironmentFile=%h/.config/knowledge-tools/%i.env",
		"Environment=KNOWLEDGE_INSTANCE=%i",
		"WantedBy=default.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("service unit missing %q\n%s", want, out)
		}
	}
}

func TestInstanceEnvContents(t *testing.T) {
	out := instanceEnvContents(testCfg())
	for _, want := range []string{
		"KNOWLEDGE_REPO=/home/me/vault",
		"KNOWLEDGE_COMPILE_SCHEDULE=@hourly",
		"KNOWLEDGE_SYNTHESIZE_SCHEDULE=CRON_TZ=America/Detroit 30 4 * * 0",
		"KNOWLEDGE_RESOLVE_SCHEDULE=30 3 * * *",
		"KNOWLEDGE_COMPILE_COOLDOWN=1800",
		"KNOWLEDGE_REVIEW_CHANNEL=files",
		"KNOWLEDGE_GITHUB_REPO=me/vault",
		"KNOWLEDGE_SITE_REBUILD_URL=http://knowledge-site:8080/rebuild",
		"KNOWLEDGE_SITE_REBUILD_TOKEN=s3cret",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env file missing %q\n%s", want, out)
		}
	}
	// KNOWLEDGE_INSTANCE comes from the unit's Environment=%i, not the env file.
	if strings.Contains(out, "KNOWLEDGE_INSTANCE=") {
		t.Errorf("env file should not set KNOWLEDGE_INSTANCE\n%s", out)
	}
}

func TestPlistContents(t *testing.T) {
	out := plistContents(Options{Cfg: testCfg(), BinPath: "/usr/local/bin/knowledge-tools"})
	for _, want := range []string{
		"<string>com.knowledge-tools.daemon.work</string>",
		"<string>/usr/local/bin/knowledge-tools</string>",
		"<string>daemon</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"<key>KNOWLEDGE_REPO</key>",
		"<string>/home/me/vault</string>",
		"<key>KNOWLEDGE_SYNTHESIZE_SCHEDULE</key>",
		"<key>KNOWLEDGE_SITE_REBUILD_URL</key>",
		"<key>KNOWLEDGE_SITE_REBUILD_TOKEN</key>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n%s", want, out)
		}
	}
}

func TestPlistEscapesXML(t *testing.T) {
	cfg := testCfg()
	cfg.Repo = "/home/me/a & b/vault"
	out := plistContents(Options{Cfg: cfg, BinPath: "/usr/bin/knowledge-tools"})
	if !strings.Contains(out, "/home/me/a &amp; b/vault") {
		t.Errorf("repo path with & not XML-escaped\n%s", out)
	}
	if strings.Contains(out, "a & b/vault") {
		t.Error("raw unescaped ampersand leaked into the plist")
	}
}

func TestLastInstance(t *testing.T) {
	dir := t.TempDir()
	// gh.env alone doesn't count as an instance.
	if err := os.WriteFile(filepath.Join(dir, "gh.env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !lastInstance(dir) {
		t.Error("only gh.env present → should be last instance")
	}
	// Add a real instance env file.
	if err := os.WriteFile(filepath.Join(dir, "work.env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if lastInstance(dir) {
		t.Error("a work.env present → not the last instance")
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscape(`a&b<c>"d"`); got != "a&amp;b&lt;c&gt;&quot;d&quot;" {
		t.Errorf("xmlEscape = %q", got)
	}
}

func TestTildify(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home dir")
	}
	want := filepath.Join("~", "x", "y")
	if got := tildify(filepath.Join(home, "x", "y")); got != want {
		t.Errorf("tildify = %q, want %q", got, want)
	}
	if got := tildify("/etc/passwd"); got != "/etc/passwd" {
		t.Errorf("tildify non-home = %q", got)
	}
}
