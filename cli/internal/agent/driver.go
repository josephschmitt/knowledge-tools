// Package agent abstracts the headless coding-agent invocation so the vault jobs can run on any
// harness — Claude Code, OpenAI Codex, OpenCode, or a user-supplied custom command — instead of
// only `claude`. The job layer builds a harness-neutral Invocation (prompt, model, effort, shell
// grants); a Driver selected by KNOWLEDGE_AGENT translates it into that harness's argv, dropping
// whatever it can't express.
//
// This replaces the old vault.RunClaude, which hardwired `claude -p <slash> --permission-mode
// acceptEdits [--allowedTools …]`. The claude driver reproduces that argv (the only addition is an
// explicit --model, which the slash-command frontmatter used to carry).
package agent

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Invocation is the harness-neutral request the job layer hands a Driver. Each driver translates
// these into its own CLI flags/config, dropping fields it can't express (e.g. Effort on harnesses
// with no reasoning-effort knob).
type Invocation struct {
	Repo        string   // working directory for the run
	Prompt      string   // full prompt text (the skill body, $ARGUMENTS already substituted)
	Model       string   // resolved model; "" lets the harness use its own default
	Effort      string   // resolved reasoning effort; "" leaves it unset (claude/codex only)
	ShellGrants []string // neutral command prefixes the run may execute unattended, e.g.
	// "gh issue list"; nil means file edits only (no shell). Drivers re-encode these into
	// their own allowlist syntax; drivers that can't scope shell ignore them (see
	// SupportsShellGrants).
}

// Driver builds the argv for one harness. Build returns the command (Path/Args/Stdin/Env set), an
// optional cleanup for any ephemeral files it materialized (nil if none), and an error. The caller
// sets Dir/Stdout/Stderr and runs it — see Run.
type Driver interface {
	Name() string
	// SupportsShellGrants reports whether the driver can scope unattended shell execution to a
	// specific allowlist. Drivers that can't (so granting any shell means granting ALL shell)
	// return false, and the job layer falls back to the grant-free files channel rather than
	// over-granting an unattended agent.
	SupportsShellGrants() bool
	Build(ctx context.Context, inv Invocation) (cmd *exec.Cmd, cleanup func(), err error)
}

// Spec is the resolved agent configuration a Driver is built from (from the KNOWLEDGE_AGENT* knobs).
type Spec struct {
	Agent string // claude | codex | opencode | custom ("" == claude)
	Bin   string // harness binary path/name; "" → the driver's own default
	Cmd   string // command template, required only when Agent == "custom"
}

// NewDriver returns the Driver for spec.Agent.
func NewDriver(spec Spec) (Driver, error) {
	switch spec.Agent {
	case "", "claude":
		return &claudeDriver{bin: spec.Bin}, nil
	case "codex":
		return &codexDriver{bin: spec.Bin}, nil
	case "opencode":
		return &opencodeDriver{bin: spec.Bin}, nil
	case "custom":
		return newCustomDriver(spec.Bin, spec.Cmd)
	default:
		return nil, fmt.Errorf("unknown KNOWLEDGE_AGENT %q (expected claude, codex, opencode, or custom)", spec.Agent)
	}
}

// Run builds the command for inv and runs it with cwd=inv.Repo, streaming combined output to out.
func Run(ctx context.Context, d Driver, inv Invocation, out io.Writer) error {
	cmd, cleanup, err := d.Build(ctx, inv)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if cmd.Dir == "" {
		cmd.Dir = inv.Repo
	}
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
