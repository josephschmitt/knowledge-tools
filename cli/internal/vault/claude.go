package vault

import (
	"context"
	"os/exec"
)

// RunClaude runs a fresh, headless claude pass: `claudeBin -p <slash> --permission-mode
// acceptEdits [--allowedTools t1 t2 ...]` with cwd=repo, streaming all output to the log file.
//
// acceptEdits auto-applies Claude's Write/Edit without prompting; Claude never runs git (the
// wrapper owns it). allowedTools grants exactly the gh subcommands the github review channel needs
// (and nothing else); pass nil for the files channel and compile, which edit files only. The slash
// commands self-declare model + effort in their frontmatter, so no --model is passed.
func RunClaude(ctx context.Context, claudeBin, repo, slash string, allowedTools []string, log *Logger) error {
	args := []string{"-p", slash, "--permission-mode", "acceptEdits"}
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, allowedTools...)
	}
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = repo
	cmd.Stdout = log.File()
	cmd.Stderr = log.File()
	return cmd.Run()
}
