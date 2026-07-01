package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// claudeDriver runs Claude Code headlessly:
//
//	claude -p <prompt> --permission-mode acceptEdits [--model <m>] [--allowedTools Bash(<grant>:*) …]
//
// reproducing the original vault.RunClaude argv. acceptEdits auto-applies Write/Edit without
// prompting; Claude never runs git (the wrapper owns it). The neutral shell grants are re-wrapped
// into Claude's Bash(<prefix>:*) tool syntax. --model is the one addition over the legacy argv: the
// slash-command frontmatter used to carry the model, but skills don't, so the job layer passes it
// explicitly (defaulting to opus for the claude agent — see config.Config.JobModel). Effort has no
// Claude CLI flag, so Invocation.Effort is dropped.
type claudeDriver struct{ bin string }

func (d *claudeDriver) Name() string              { return "claude" }
func (d *claudeDriver) SupportsShellGrants() bool { return true }

func (d *claudeDriver) Build(ctx context.Context, inv Invocation) (*exec.Cmd, func(), error) {
	args := []string{"-p", inv.Prompt, "--permission-mode", "acceptEdits"}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if len(inv.ShellGrants) > 0 {
		args = append(args, "--allowedTools")
		for _, g := range inv.ShellGrants {
			args = append(args, "Bash("+g+":*)")
		}
	}
	return exec.CommandContext(ctx, d.binOrDefault(), args...), nil, nil
}

// binOrDefault preserves the legacy default of ~/.local/bin/claude when no binary is configured.
func (d *claudeDriver) binOrDefault() string {
	if d.bin != "" {
		return d.bin
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "claude")
}
