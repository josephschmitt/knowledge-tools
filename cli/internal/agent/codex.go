package agent

import (
	"context"
	"os/exec"
)

// codexDriver runs OpenAI Codex headlessly:
//
//	codex exec <prompt> --full-auto [-m <model>] [-c model_reasoning_effort=<effort>]
//
// --full-auto runs unattended (workspace-write sandbox, no approval prompts). Codex's
// sandbox/approval is all-or-nothing — it cannot scope shell to specific commands — so
// SupportsShellGrants is false and the job layer routes Codex to the grant-free files channel
// rather than over-granting it the github channel's gh access.
//
// NOTE: Codex CLI flag spellings (`exec`, `--full-auto`, the `-c model_reasoning_effort=` override)
// track Codex releases and have drifted before; re-verify on upgrade. Effort values are Codex's
// (low|medium|high) — the old slash frontmatter's "xhigh" is not a valid Codex effort.
type codexDriver struct{ bin string }

func (d *codexDriver) Name() string              { return "codex" }
func (d *codexDriver) SupportsShellGrants() bool { return false }

func (d *codexDriver) Build(ctx context.Context, inv Invocation) (*exec.Cmd, func(), error) {
	args := []string{"exec", inv.Prompt, "--full-auto"}
	if inv.Model != "" {
		args = append(args, "-m", inv.Model)
	}
	if inv.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+inv.Effort)
	}
	return exec.CommandContext(ctx, d.binOrDefault(), args...), nil, nil
}

func (d *codexDriver) binOrDefault() string {
	if d.bin != "" {
		return d.bin
	}
	return "codex"
}
