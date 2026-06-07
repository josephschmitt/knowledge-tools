#!/usr/bin/env bash
# Install the knowledge-vault compile automation as systemd *user* units.
#
# Generates three user units from the templates next to this script and enables them.
# Two paths are filled in (they live in different repos now that tooling and the vault
# are split):
#   - @TOOLS_REPO@ — this repo, where the worker script lives (ExecStart).
#   - @VAULT_REPO@ — the knowledge-vault repo, whose inbox is watched and compiled.
#                    Defaults to ~/knowledge-vault; override with KNOWLEDGE_REPO.
# The three units:
#   - knowledge-compile.service  one ephemeral inbox->wiki compile (the worker)
#   - knowledge-compile.timer    runs it nightly at 03:00
#   - knowledge-compile.path     runs it on demand when the MCP server drops
#                                inbox/.compile/request into the vault (compile_run tool)
#
# Idempotent — safe to re-run (re-run after editing a *.in template). Run from anywhere:
#   ~/development/knowledge-tools/scripts/install.sh
#   KNOWLEDGE_REPO=/path/to/knowledge-vault ~/development/knowledge-tools/scripts/install.sh
set -euo pipefail

SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLS_REPO="$(cd "$SCRIPTS/.." && pwd)"
VAULT_REPO="${KNOWLEDGE_REPO:-$HOME/knowledge-vault}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNITS=(knowledge-compile.service knowledge-compile.timer knowledge-compile.path)

if ! systemctl --user show-environment >/dev/null 2>&1; then
  echo "error: systemd user instance isn't available. Are you on the host as your user?" >&2
  exit 1
fi

if [ ! -d "$VAULT_REPO" ]; then
  echo "warning: vault repo not found at $VAULT_REPO (set KNOWLEDGE_REPO to override)." >&2
fi

echo "Generating units in $UNIT_DIR (tools: $TOOLS_REPO, vault: $VAULT_REPO)"
mkdir -p "$UNIT_DIR"
for u in "${UNITS[@]}"; do
  sed -e "s|@TOOLS_REPO@|$TOOLS_REPO|g" -e "s|@VAULT_REPO@|$VAULT_REPO|g" \
    "$SCRIPTS/$u.in" >"$UNIT_DIR/$u"
  echo "  $u"
done

echo "Reloading the user daemon"
systemctl --user daemon-reload

echo "Enabling + starting the timer and path watcher"
systemctl --user enable --now knowledge-compile.timer knowledge-compile.path

# Linger lets the units run while you're logged out (the nightly timer especially).
if loginctl show-user "$USER" --property=Linger 2>/dev/null | grep -q 'Linger=yes'; then
  echo "Linger already enabled"
elif loginctl enable-linger "$USER" 2>/dev/null; then
  echo "Enabled linger for $USER"
else
  echo "note: couldn't enable linger automatically — run: sudo loginctl enable-linger $USER"
fi

echo
echo "Done. Status:"
systemctl --user list-timers knowledge-compile.timer --no-pager || true
systemctl --user status knowledge-compile.path --no-pager --lines=0 || true
echo
echo "Tip: trigger a manual compile to test the path watcher:"
echo "  date -Is > $VAULT_REPO/inbox/.compile/request"
echo "  journalctl --user -u knowledge-compile.service -f"
