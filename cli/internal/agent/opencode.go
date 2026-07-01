package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// opencodeDriver runs OpenCode headlessly:
//
//	opencode run <prompt> [-m <provider/model>]
//
// OpenCode has no auto-approve CLI flag, so unattended permissions are expressed through a config
// file: the driver materializes an ephemeral opencode.json in a temp dir and points OpenCode at it
// via OPENCODE_CONFIG, granting file edits and denying shell (so the run can't stall on a prompt).
// The temp dir is removed by the returned cleanup.
//
// SupportsShellGrants is false: OpenCode's bash-permission matching precedence isn't verified, and a
// naive allowlist map risks a catch-all deny shadowing the specific allows (or vice-versa) depending
// on that precedence — so rather than ship an unverifiable ACL, OpenCode joins codex on the
// grant-free files channel (which needs no shell). Effort has no OpenCode knob and is dropped.
//
// NOTE: OpenCode's run flags and the OPENCODE_CONFIG/permission schema track its releases;
// re-verify on upgrade (and revisit shell-grant support once the bash-permission precedence is
// confirmed).
type opencodeDriver struct{ bin string }

func (d *opencodeDriver) Name() string              { return "opencode" }
func (d *opencodeDriver) SupportsShellGrants() bool { return false }

func (d *opencodeDriver) Build(ctx context.Context, inv Invocation) (*exec.Cmd, func(), error) {
	cfgPath, cleanup, err := writeOpencodeConfig()
	if err != nil {
		return nil, nil, err
	}
	args := []string{"run", inv.Prompt}
	if inv.Model != "" {
		args = append(args, "-m", inv.Model)
	}
	cmd := exec.CommandContext(ctx, d.binOrDefault(), args...)
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+cfgPath)
	return cmd, cleanup, nil
}

func (d *opencodeDriver) binOrDefault() string {
	if d.bin != "" {
		return d.bin
	}
	return "opencode"
}

// writeOpencodeConfig writes an ephemeral opencode.json that auto-allows file edits and denies
// shell (the files channel never shells out — the wrapper owns git), returning its path and a
// cleanup that removes the temp dir. Simple string permissions, so there's no rule-ordering to get
// wrong.
func writeOpencodeConfig() (string, func(), error) {
	dir, err := os.MkdirTemp("", "knowledge-opencode-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	cfg := map[string]any{
		"permission": map[string]any{
			"edit": "allow",
			"bash": "deny",
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
