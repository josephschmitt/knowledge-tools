package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// customDriver runs a user-supplied command template (KNOWLEDGE_AGENT_CMD) for any harness without
// a built-in driver. The template is argv-tokenized, NOT shell-evaluated: it's split on whitespace
// into argv, then each token is substituted individually, so {{prompt}} becomes exactly one argv
// element even though the prompt body contains spaces and newlines — no quoting or injection risk.
//
// Placeholders:
//
//	{{bin}}          → the configured binary (KNOWLEDGE_AGENT_BIN)
//	{{prompt}}       → the prompt as one argv element
//	{{prompt_stdin}} → pipe the prompt via stdin; contributes no argv element
//	{{repo}}         → the working directory
//	{{model}}        → the model, or drop a preceding flag token if empty
//	{{effort}}       → the effort, or drop a preceding flag token if empty
//	{{grants}}       → zero or more argv elements (the neutral shell-grant prefixes), or drop a
//	                   preceding flag token if there are none
//
// A value placeholder ({{model}}/{{effort}}/{{grants}}) that resolves to nothing also drops the
// immediately preceding literal flag (a token starting with "-"), so `--model {{model}}` with no
// model emits neither token. SupportsShellGrants is true only when the template references
// {{grants}} (otherwise grants have nowhere to go, and the job layer routes to the files channel).
type customDriver struct {
	bin    string
	tmpl   string
	grants bool
}

func newCustomDriver(bin, tmpl string) (*customDriver, error) {
	if strings.TrimSpace(tmpl) == "" {
		return nil, fmt.Errorf("KNOWLEDGE_AGENT=custom requires KNOWLEDGE_AGENT_CMD (a command template)")
	}
	return &customDriver{bin: bin, tmpl: tmpl, grants: strings.Contains(tmpl, "{{grants}}")}, nil
}

func (d *customDriver) Name() string              { return "custom" }
func (d *customDriver) SupportsShellGrants() bool { return d.grants }

func (d *customDriver) Build(ctx context.Context, inv Invocation) (*exec.Cmd, func(), error) {
	var argv []string
	var stdin *strings.Reader
	for _, tok := range strings.Fields(d.tmpl) {
		switch tok {
		case "{{bin}}":
			argv = append(argv, d.bin)
		case "{{prompt}}":
			argv = append(argv, inv.Prompt)
		case "{{prompt_stdin}}":
			stdin = strings.NewReader(inv.Prompt)
		case "{{repo}}":
			argv = append(argv, inv.Repo)
		case "{{model}}":
			if inv.Model == "" {
				argv = dropTrailingFlag(argv)
				continue
			}
			argv = append(argv, inv.Model)
		case "{{effort}}":
			if inv.Effort == "" {
				argv = dropTrailingFlag(argv)
				continue
			}
			argv = append(argv, inv.Effort)
		case "{{grants}}":
			if len(inv.ShellGrants) == 0 {
				argv = dropTrailingFlag(argv)
				continue
			}
			argv = append(argv, inv.ShellGrants...)
		default:
			argv = append(argv, tok)
		}
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, nil, fmt.Errorf("KNOWLEDGE_AGENT_CMD produced an empty command (set KNOWLEDGE_AGENT_BIN or hardcode the binary in the template)")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	return cmd, nil, nil
}

// dropTrailingFlag removes the last argv element if it's a flag (starts with "-"), used to elide an
// empty `--flag {{value}}` pair when the value resolves to nothing.
func dropTrailingFlag(argv []string) []string {
	if n := len(argv); n > 0 && strings.HasPrefix(argv[n-1], "-") {
		return argv[:n-1]
	}
	return argv
}
