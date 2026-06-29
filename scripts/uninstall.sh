#!/usr/bin/env bash
# Uninstall the vault automation for ONE vault — the reverse of install.sh, and OS-conditional the
# same way: systemd user units on Linux, launchd LaunchAgents on macOS. Per-instance
# (KNOWLEDGE_INSTANCE, default "default") and idempotent (a no-op if that instance isn't installed).
#
# Removes ONLY the tooling artifacts install.sh creates (units/agents, the per-vault env file, and
# — when the LAST instance goes — the shared systemd service templates). It NEVER touches the vault
# itself (inbox/, library/, outputs/ and their logs) or linger. Unlike install.sh it needs no
# KNOWLEDGE_REPO, since nothing it removes lives inside the vault.
#
#   scripts/uninstall.sh                           # remove the "default" vault's jobs
#   KNOWLEDGE_INSTANCE=work scripts/uninstall.sh   # remove the "work" vault's jobs
set -euo pipefail

SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Load the repo-root .env so KNOWLEDGE_INSTANCE can come from there too (real env vars win). This
# does NOT require KNOWLEDGE_REPO the way install.sh does — uninstall touches no vault content.
. "$SCRIPTS/load-env.sh"

INSTANCE="${KNOWLEDGE_INSTANCE:-default}"
case "$INSTANCE" in
  '' | *[!A-Za-z0-9_-]*)
    echo "error: KNOWLEDGE_INSTANCE='$INSTANCE' must be a non-empty slug of [A-Za-z0-9_-]." >&2
    exit 1
    ;;
esac

# ---------------------------------------------------------------------------------------------
# Linux: systemd user units.
# ---------------------------------------------------------------------------------------------
uninstall_systemd() {
  local UNIT_DIR CONFIG_DIR INSTANCE_ENV
  UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/knowledge-tools"
  INSTANCE_ENV="$CONFIG_DIR/$INSTANCE.env"
  local TIMERS=(
    "knowledge-compile@$INSTANCE.timer"
    "knowledge-synthesize@$INSTANCE.timer"
    "knowledge-resolve@$INSTANCE.timer"
  )
  local PATH_UNIT="knowledge-compile@$INSTANCE.path"
  local SERVICE_TEMPLATES=(knowledge-compile@.service knowledge-synthesize@.service knowledge-resolve@.service)

  if ! systemctl --user show-environment >/dev/null 2>&1; then
    echo "error: systemd user instance isn't available. Are you on the host as your user?" >&2
    exit 1
  fi

  echo "Uninstalling vault '$INSTANCE' (systemd)"
  # Stop + disable this instance's timers and path watcher (ignore whatever's already gone).
  systemctl --user disable --now "${TIMERS[@]}" "$PATH_UNIT" 2>/dev/null || true

  local removed=0 u
  for u in "${TIMERS[@]}" "$PATH_UNIT"; do
    if [ -e "$UNIT_DIR/$u" ]; then rm -f "$UNIT_DIR/$u"; echo "  removed $u"; removed=1; fi
  done
  if [ -e "$INSTANCE_ENV" ]; then rm -f "$INSTANCE_ENV"; echo "  removed ${INSTANCE_ENV/#$HOME/\~}"; removed=1; fi

  # Last instance? If no per-instance compile timer remains, the shared service templates are now
  # orphaned, so remove them too (and the config dir if it's emptied — gh.env etc. keep it).
  shopt -s nullglob
  local remaining=("$UNIT_DIR"/knowledge-compile@?*.timer)
  shopt -u nullglob
  if [ "${#remaining[@]}" -eq 0 ]; then
    for u in "${SERVICE_TEMPLATES[@]}"; do
      if [ -e "$UNIT_DIR/$u" ]; then rm -f "$UNIT_DIR/$u"; echo "  removed $u (last instance — shared template)"; removed=1; fi
    done
    rmdir "$CONFIG_DIR" 2>/dev/null && echo "  removed ${CONFIG_DIR/#$HOME/\~} (empty)" || true
  fi

  if [ "$removed" = 1 ]; then
    systemctl --user daemon-reload
    echo "Done."
  else
    echo "Nothing to remove for instance '$INSTANCE'."
  fi
}

# ---------------------------------------------------------------------------------------------
# macOS: launchd LaunchAgents. Each plist is self-contained (no shared templates), so per-instance
# removal is the whole story; on the last instance we just drop the now-empty logs dir.
# ---------------------------------------------------------------------------------------------
uninstall_launchd() {
  local LA_DIR LOG_DIR_M uid
  LA_DIR="$HOME/Library/LaunchAgents"
  LOG_DIR_M="$HOME/Library/Logs/knowledge-tools"
  uid="$(id -u)"

  echo "Uninstalling vault '$INSTANCE' (launchd)"
  local removed=0 job label dest log
  for job in compile synthesize resolve; do
    label="com.knowledge-tools.$job.$INSTANCE"
    dest="$LA_DIR/$label.plist"
    log="$LOG_DIR_M/$INSTANCE-$job.log"
    launchctl bootout "gui/$uid/$label" 2>/dev/null || true
    if [ -e "$dest" ]; then rm -f "$dest"; echo "  removed ${dest/#$HOME/\~}"; removed=1; fi
    if [ -e "$log" ]; then rm -f "$log"; echo "  removed ${log/#$HOME/\~}"; removed=1; fi
  done
  # Last instance? Key off surviving plists, NOT an empty logs dir — agents only write logs once
  # they fire, so an empty dir would falsely flag "last" on a never-run multi-instance host (and
  # delete the shared dir out from under the other still-installed agents). No shared templates on
  # macOS, so the only shared cleanup is dropping the now-orphaned (empty) logs dir.
  shopt -s nullglob
  local remaining=("$LA_DIR"/com.knowledge-tools.compile.*.plist)
  shopt -u nullglob
  if [ "${#remaining[@]}" -eq 0 ]; then
    rmdir "$LOG_DIR_M" 2>/dev/null && echo "  removed ${LOG_DIR_M/#$HOME/\~} (empty)" || true
  fi
  if [ "$removed" = 1 ]; then echo "Done."; else echo "Nothing to remove for instance '$INSTANCE'."; fi
}

OS="$(uname -s)"
case "$OS" in
  Linux)  uninstall_systemd ;;
  Darwin) uninstall_launchd ;;
  *) echo "error: unsupported OS '$OS' — need Linux (systemd) or macOS (launchd)." >&2; exit 1 ;;
esac

echo "(Left untouched: the vault itself — inbox/, library/, outputs/ — and linger.)"
