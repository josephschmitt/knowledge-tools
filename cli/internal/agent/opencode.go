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
// file. The driver materializes an ephemeral opencode.json in a temp dir and points OpenCode at it
// via OPENCODE_CONFIG, granting file edits unconditionally and scoping bash to the neutral shell
// grants (everything else denied, so the run can't stall on a prompt). Because it CAN scope bash,
// SupportsShellGrants is true and OpenCode may serve the github channel. The temp dir is removed by
// the returned cleanup. Effort has no OpenCode knob and is dropped.
//
// NOTE: OpenCode's run flags and the OPENCODE_CONFIG/permission schema track its releases;
// re-verify on upgrade.
type opencodeDriver struct{ bin string }

func (d *opencodeDriver) Name() string              { return "opencode" }
func (d *opencodeDriver) SupportsShellGrants() bool { return true }

func (d *opencodeDriver) Build(_ context.Context, inv Invocation) (*exec.Cmd, func(), error) {
	cfgPath, cleanup, err := writeOpencodeConfig(inv.ShellGrants)
	if err != nil {
		return nil, nil, err
	}
	args := []string{"run", inv.Prompt}
	if inv.Model != "" {
		args = append(args, "-m", inv.Model)
	}
	cmd := exec.Command(d.binOrDefault(), args...)
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+cfgPath)
	return cmd, cleanup, nil
}

func (d *opencodeDriver) binOrDefault() string {
	if d.bin != "" {
		return d.bin
	}
	return "opencode"
}

// writeOpencodeConfig writes an ephemeral opencode.json that auto-allows edits and scopes bash to
// the given neutral grant prefixes (denying everything else), returning its path and a cleanup that
// removes the temp dir.
func writeOpencodeConfig(grants []string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "knowledge-opencode-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	bash := map[string]string{}
	for _, g := range grants {
		bash[g+" *"] = "allow"
	}
	bash["*"] = "deny"

	cfg := map[string]any{
		"permission": map[string]any{
			"edit": "allow",
			"bash": bash,
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
