#!/usr/bin/env bash
# Install the vault compile automation as systemd *user* units.
#
# Generates the user units from the templates next to this script and enables them.
# Two paths are filled in (they live in different repos now that tooling and the vault
# are split):
#   - @TOOLS_REPO@ — this repo, where the worker scripts live (ExecStart).
#   - @VAULT_REPO@ — the vault repo, whose inbox is watched and whose wiki is operated on.
#                    Its path comes from the required KNOWLEDGE_REPO env var.
# The units:
#   - knowledge-compile.service     one ephemeral inbox->wiki compile (the worker)
#   - knowledge-compile.timer       runs it on a schedule (KNOWLEDGE_COMPILE_ONCALENDAR,
#                                   default hourly; no-ops cheaply when the inbox is empty)
#   - knowledge-compile.path        runs it on demand when the MCP server drops
#                                   inbox/.compile/request into the vault (compile_run tool)
#   - knowledge-synthesize.service  one whole-corpus /synthesize pass (reconcile + connect,
#                                   opens judgment-call issues)
#   - knowledge-synthesize.timer    runs it on a schedule (KNOWLEDGE_SYNTHESIZE_ONCALENDAR,
#                                   default weekly) — heavy; keep it rare
#   - knowledge-resolve.service     one /resolve pass (applies my issue answers, closes them)
#   - knowledge-resolve.timer       runs it on a schedule (KNOWLEDGE_RESOLVE_ONCALENDAR,
#                                   default daily) — light; short-circuits when no open issues
#
# synthesize/resolve run `gh` from inside their Claude run, so they need gh on PATH and a
# logged-in `gh auth` (~/.config/gh); the generated service units set PATH and rely on HOME.
# All three vault jobs share one lockfile (see vault-lib.sh) so they never run concurrently.
#
# KNOWLEDGE_REPO is required — point it at the vault repo. Idempotent — safe to re-run
# (re-run after editing a *.in template). Run from anywhere:
#   KNOWLEDGE_REPO=/path/to/vault ~/development/knowledge-tools/scripts/install.sh
set -euo pipefail

SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLS_REPO="$(cd "$SCRIPTS/.." && pwd)"
# Load config from the repo-root .env (KNOWLEDGE_REPO etc.); real env vars take precedence.
. "$SCRIPTS/load-env.sh"
: "${KNOWLEDGE_REPO:?set KNOWLEDGE_REPO to the vault repo path (in .env or the environment)}"
VAULT_REPO="$KNOWLEDGE_REPO"
# Cadences — any systemd OnCalendar expression (e.g. hourly, daily, weekly, '*-*-* 03:00:00',
# '*-*-* *:00/30:00' for every 30 min; an optional trailing timezone like 'America/Detroit' is
# honored on systemd >= 250). The compile worker no-ops on an empty inbox; resolve short-circuits
# with no open issues — so a frequent cadence for those only does real work when there's
# something to do. synthesize is heavy whole-corpus, so it defaults rare.
#
# The two Claude-credit-hungry jobs (synthesize/resolve) default to the *middle of the night*
# (pinned to America/Detroit so it tracks local night through DST, even on a UTC host) to land
# on off-peak Max-plan capacity. They're staggered onto the half-hour and an hour apart so they
# don't collide with the hourly compile's top-of-hour fire window or each other — colliding jobs
# don't queue, they bail on the shared lock (and skip that tick).
ONCALENDAR="${KNOWLEDGE_COMPILE_ONCALENDAR:-hourly}"
SYNTH_ONCALENDAR="${KNOWLEDGE_SYNTHESIZE_ONCALENDAR:-Sun *-*-* 04:30:00 America/Detroit}"
RESOLVE_ONCALENDAR="${KNOWLEDGE_RESOLVE_ONCALENDAR:-*-*-* 03:30:00 America/Detroit}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNITS=(
  knowledge-compile.service knowledge-compile.timer knowledge-compile.path
  knowledge-synthesize.service knowledge-synthesize.timer
  knowledge-resolve.service knowledge-resolve.timer
)
# Timers we enable + start (the .path watcher is enabled alongside them below).
TIMERS=(knowledge-compile.timer knowledge-synthesize.timer knowledge-resolve.timer)

if ! systemctl --user show-environment >/dev/null 2>&1; then
  echo "error: systemd user instance isn't available. Are you on the host as your user?" >&2
  exit 1
fi

# Fail fast on a bad cadence rather than letting systemd reject the unit later.
check_oncalendar() { # <env-var-name> <value>
  if command -v systemd-analyze >/dev/null 2>&1 && ! systemd-analyze calendar "$2" >/dev/null 2>&1; then
    echo "error: $1='$2' is not a valid systemd OnCalendar expression." >&2
    echo "  Examples: hourly | daily | weekly | '*-*-* 03:00:00' | '*-*-* *:00/30:00' (every 30 min)" >&2
    exit 1
  fi
}
check_oncalendar KNOWLEDGE_COMPILE_ONCALENDAR "$ONCALENDAR"
check_oncalendar KNOWLEDGE_SYNTHESIZE_ONCALENDAR "$SYNTH_ONCALENDAR"
check_oncalendar KNOWLEDGE_RESOLVE_ONCALENDAR "$RESOLVE_ONCALENDAR"

# synthesize/resolve run `gh` from inside their Claude run; warn (don't fail) if the host
# isn't set up for that, since compile doesn't need it and a fresh install may add gh later.
if ! command -v gh >/dev/null 2>&1; then
  echo "warning: 'gh' not found on PATH — synthesize/resolve can't file or close issues until" >&2
  echo "  the GitHub CLI is installed and on the service PATH (the units add ~/.nix-profile/bin)." >&2
elif ! gh auth status >/dev/null 2>&1; then
  echo "warning: 'gh' is not authenticated — run 'gh auth login' so synthesize/resolve can use it." >&2
fi

if [ ! -d "$VAULT_REPO" ]; then
  echo "warning: vault repo not found at $VAULT_REPO (from KNOWLEDGE_REPO)." >&2
fi

echo "Generating units in $UNIT_DIR (tools: $TOOLS_REPO, vault: $VAULT_REPO)"
echo "  cadence: compile=$ONCALENDAR synthesize=$SYNTH_ONCALENDAR resolve=$RESOLVE_ONCALENDAR"
mkdir -p "$UNIT_DIR"
for u in "${UNITS[@]}"; do
  sed -e "s|@TOOLS_REPO@|$TOOLS_REPO|g" -e "s|@VAULT_REPO@|$VAULT_REPO|g" \
    -e "s|@ONCALENDAR@|$ONCALENDAR|g" \
    -e "s|@SYNTH_ONCALENDAR@|$SYNTH_ONCALENDAR|g" \
    -e "s|@RESOLVE_ONCALENDAR@|$RESOLVE_ONCALENDAR|g" \
    "$SCRIPTS/$u.in" >"$UNIT_DIR/$u"
  echo "  $u"
done

echo "Reloading the user daemon"
systemctl --user daemon-reload

echo "Enabling + starting the timers and path watcher"
systemctl --user enable --now "${TIMERS[@]}" knowledge-compile.path

# Linger lets the units run while you're logged out (the scheduled timers especially).
if loginctl show-user "$USER" --property=Linger 2>/dev/null | grep -q 'Linger=yes'; then
  echo "Linger already enabled"
elif loginctl enable-linger "$USER" 2>/dev/null; then
  echo "Enabled linger for $USER"
else
  echo "note: couldn't enable linger automatically — run: sudo loginctl enable-linger $USER"
fi

echo
echo "Done. Status:"
systemctl --user list-timers "${TIMERS[@]}" --no-pager || true
systemctl --user status knowledge-compile.path --no-pager --lines=0 || true
echo
echo "Tips:"
echo "  # trigger a manual compile (exercises the path watcher):"
echo "  date -Is > $VAULT_REPO/inbox/.compile/request"
echo "  journalctl --user -u knowledge-compile.service -f"
echo "  # run synthesize / resolve on demand:"
echo "  systemctl --user start knowledge-synthesize.service   # journalctl --user -u knowledge-synthesize.service -f"
echo "  systemctl --user start knowledge-resolve.service      # journalctl --user -u knowledge-resolve.service -f"
