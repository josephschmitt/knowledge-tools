#!/usr/bin/env bash
# Install the knowledge-vault compile automation as systemd *user* units.
#
# Sets up everything the host needs to compile the inbox into the wiki:
#   - knowledge-compile.service  one ephemeral inbox->wiki compile (the worker)
#   - knowledge-compile.timer    runs it nightly at 03:00
#   - knowledge-compile.path     runs it on demand when the MCP server drops
#                                inbox/.compile/request (the compile_run tool)
#
# Idempotent — safe to re-run. Run it from anywhere; it finds the repo itself:
#   ~/knowledge/deploy/install.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY="$REPO/deploy"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNITS=(knowledge-compile.service knowledge-compile.timer knowledge-compile.path)

# The unit files hardcode an absolute path (systemd requires one). If the repo lives
# somewhere else, the units won't point at it — bail with a clear message rather than
# installing something broken.
EXPECTED="/home/USER/knowledge"
if [ "$REPO" != "$EXPECTED" ]; then
  echo "warning: repo is at $REPO but the unit files hardcode $EXPECTED." >&2
  echo "         Edit the ExecStart / PathExists paths in $DEPLOY/*.{service,path} to match," >&2
  echo "         then re-run this script." >&2
  exit 1
fi

if ! systemctl --user show-environment >/dev/null 2>&1; then
  echo "error: systemd user instance isn't available. Are you on the host as your user?" >&2
  exit 1
fi

echo "Linking units into $UNIT_DIR"
mkdir -p "$UNIT_DIR"
for u in "${UNITS[@]}"; do
  ln -sf "$DEPLOY/$u" "$UNIT_DIR/$u"
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
echo "  date -Is > $REPO/inbox/.compile/request"
echo "  journalctl --user -u knowledge-compile.service -f"
