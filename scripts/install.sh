#!/usr/bin/env bash
# Install the vault automation as systemd *user* units — for ONE vault per run.
#
# Multi-vault by deployment: each vault is a systemd template INSTANCE named by KNOWLEDGE_INSTANCE
# (default "default" for a single-vault host). Re-run this script once per vault to add another;
# the instances are independent (own config, own lock, own schedule) and run concurrently.
#
# Two repo paths are filled into the generated units (tooling and the vault live in separate repos):
#   - @TOOLS_REPO@ — this repo, where the worker scripts live (ExecStart).
#   - @VAULT_REPO@ — the vault repo, whose inbox is watched and whose wiki is operated on
#                    (from the required KNOWLEDGE_REPO). Only the per-vault .path unit bakes this
#                    path in; the service/timer units read it from the per-vault env file instead.
#
# What gets generated per run:
#   - Shared service TEMPLATES (written once, reused by every instance):
#       knowledge-compile@.service     one ephemeral inbox->wiki compile (the worker)
#       knowledge-synthesize@.service  one whole-corpus /synthesize pass (opens judgment-call issues)
#       knowledge-resolve@.service     one /resolve pass (applies my answers, closes issues)
#     Each reads this vault's KNOWLEDGE_REPO from %h/.config/knowledge-tools/%i.env.
#   - Per-vault units (written for THIS instance, carrying its schedule / watched path):
#       knowledge-compile@<inst>.timer + knowledge-compile@<inst>.path
#       knowledge-synthesize@<inst>.timer
#       knowledge-resolve@<inst>.timer
#   - This vault's config file %h/.config/knowledge-tools/<inst>.env (KNOWLEDGE_REPO + optional
#     per-vault KNOWLEDGE_REVIEW_CHANNEL / KNOWLEDGE_GITHUB_REPO / KNOWLEDGE_COMPILE_COOLDOWN).
#
# synthesize/resolve run `gh` from inside their Claude run, so they need gh on PATH and a logged-in
# `gh auth` (~/.config/gh); the generated service units set PATH and rely on HOME. Each vault's three
# jobs share one per-instance lockfile (see vault-lib.sh) so they never run concurrently — but
# different vaults DO run concurrently.
#
# KNOWLEDGE_REPO is required — point it at the vault repo. Idempotent — safe to re-run (re-run after
# editing a *.in template or to change a cadence). Run from anywhere:
#   KNOWLEDGE_REPO=/path/to/vault                         scripts/install.sh   # single vault ("default")
#   KNOWLEDGE_INSTANCE=work KNOWLEDGE_REPO=/path/to/work  scripts/install.sh   # add another vault
set -euo pipefail

SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLS_REPO="$(cd "$SCRIPTS/.." && pwd)"
# Load config from the repo-root .env (KNOWLEDGE_REPO etc.); real env vars take precedence.
. "$SCRIPTS/load-env.sh"
: "${KNOWLEDGE_REPO:?set KNOWLEDGE_REPO to the vault repo path (in .env or the environment)}"
VAULT_REPO="$KNOWLEDGE_REPO"

# The vault instance — one systemd template instance per vault. Default "default" so a single-vault
# host needs no extra config (and the existing user just re-runs install once to migrate). Restrict
# to a filename/systemd-safe slug so it needs no instance-name escaping.
INSTANCE="${KNOWLEDGE_INSTANCE:-default}"
case "$INSTANCE" in
  '' | *[!A-Za-z0-9_-]*)
    echo "error: KNOWLEDGE_INSTANCE='$INSTANCE' must be a non-empty slug of [A-Za-z0-9_-]." >&2
    exit 1
    ;;
esac

# Cadences — any systemd OnCalendar expression (e.g. hourly, daily, weekly, '*-*-* 03:00:00',
# '*-*-* *:00/30:00' for every 30 min; an optional trailing timezone like 'America/Detroit' is
# honored on systemd >= 250). The compile worker no-ops on an empty inbox; resolve short-circuits
# with no open issues — so a frequent cadence for those only does real work when there's
# something to do. synthesize is heavy whole-corpus, so it defaults rare. Each vault keeps its own
# cadence (set these per run alongside KNOWLEDGE_INSTANCE).
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
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/knowledge-tools"
INSTANCE_ENV="$CONFIG_DIR/$INSTANCE.env"

# Shared service templates (instance-independent: KNOWLEDGE_REPO comes from the per-vault env file).
SERVICE_TEMPLATES=(knowledge-compile@.service knowledge-synthesize@.service knowledge-resolve@.service)
# This vault's timers + compile path watcher (carry the schedule / watched path for this instance).
TIMERS=(
  "knowledge-compile@$INSTANCE.timer"
  "knowledge-synthesize@$INSTANCE.timer"
  "knowledge-resolve@$INSTANCE.timer"
)
PATH_UNIT="knowledge-compile@$INSTANCE.path"
# Legacy single-vault units from before multi-vault — removed on migration so they don't double-fire.
LEGACY_UNITS=(
  knowledge-compile.service knowledge-compile.timer knowledge-compile.path
  knowledge-synthesize.service knowledge-synthesize.timer
  knowledge-resolve.service knowledge-resolve.timer
)

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

echo "Installing vault '$INSTANCE' (tools: $TOOLS_REPO, vault: $VAULT_REPO)"
echo "  cadence: compile=$ONCALENDAR synthesize=$SYNTH_ONCALENDAR resolve=$RESOLVE_ONCALENDAR"
mkdir -p "$UNIT_DIR" "$CONFIG_DIR"

# Write this vault's config file (read by its service units via EnvironmentFile). Install-managed —
# re-running rewrites it from the current environment. 0600 since it names the vault path/repo.
echo "  config: $INSTANCE_ENV"
# Subshell the umask so the restrictive mode applies ONLY to this write — otherwise it leaks into
# the render() calls below and the generated unit files land 600 instead of the conventional 644.
( umask 077
  {
    echo "# Managed by scripts/install.sh — per-vault config for instance '$INSTANCE'."
    echo "# Loaded by the knowledge-*@$INSTANCE units via EnvironmentFile. Re-running install.sh rewrites it."
    echo "KNOWLEDGE_REPO=$VAULT_REPO"
    [ -n "${KNOWLEDGE_REVIEW_CHANNEL:-}" ] && echo "KNOWLEDGE_REVIEW_CHANNEL=$KNOWLEDGE_REVIEW_CHANNEL"
    [ -n "${KNOWLEDGE_GITHUB_REPO:-}" ] && echo "KNOWLEDGE_GITHUB_REPO=$KNOWLEDGE_GITHUB_REPO"
    [ -n "${KNOWLEDGE_COMPILE_COOLDOWN:-}" ] && echo "KNOWLEDGE_COMPILE_COOLDOWN=$KNOWLEDGE_COMPILE_COOLDOWN"
    true
  } >"$INSTANCE_ENV"
)
chmod 600 "$INSTANCE_ENV"

# Render a *.in template with all placeholders substituted. The service templates use only
# @TOOLS_REPO@; the per-vault timer/path units also consume @VAULT_REPO@ and the cadences.
render() { # <src basename under SCRIPTS> <dest basename under UNIT_DIR>
  sed -e "s|@TOOLS_REPO@|$TOOLS_REPO|g" -e "s|@VAULT_REPO@|$VAULT_REPO|g" \
    -e "s|@ONCALENDAR@|$ONCALENDAR|g" \
    -e "s|@SYNTH_ONCALENDAR@|$SYNTH_ONCALENDAR|g" \
    -e "s|@RESOLVE_ONCALENDAR@|$RESOLVE_ONCALENDAR|g" \
    "$SCRIPTS/$1" >"$UNIT_DIR/$2"
  echo "  $2"
}

echo "Generating units in $UNIT_DIR"
for t in "${SERVICE_TEMPLATES[@]}"; do
  render "$t.in" "$t"
done
render knowledge-compile@.timer.in    "knowledge-compile@$INSTANCE.timer"
render knowledge-compile@.path.in     "$PATH_UNIT"
render knowledge-synthesize@.timer.in "knowledge-synthesize@$INSTANCE.timer"
render knowledge-resolve@.timer.in    "knowledge-resolve@$INSTANCE.timer"

# Migrate a pre-multi-vault install: the old non-instanced units map to THIS run's @default units
# (same vault, same schedule), so they'd double-fire. Only remove them when installing the default
# instance — that's the run that writes the replacements. Installing a NON-default vault first (to
# add a second vault before migrating the original) must leave the legacy units running until the
# user re-runs for `default`; otherwise the original vault's automation would silently stop.
if [ "$INSTANCE" = default ]; then
  legacy_found=0
  for u in "${LEGACY_UNITS[@]}"; do
    [ -e "$UNIT_DIR/$u" ] && legacy_found=1
  done
  if [ "$legacy_found" = 1 ]; then
    echo "Migrating: removing legacy non-instanced units (replaced by @default units)"
    systemctl --user disable --now \
      knowledge-compile.timer knowledge-synthesize.timer knowledge-resolve.timer knowledge-compile.path \
      2>/dev/null || true
    for u in "${LEGACY_UNITS[@]}"; do rm -f "$UNIT_DIR/$u"; done
  fi
fi

echo "Reloading the user daemon"
systemctl --user daemon-reload

echo "Enabling + starting the '$INSTANCE' timers and path watcher"
systemctl --user enable --now "${TIMERS[@]}" "$PATH_UNIT"

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
systemctl --user status "$PATH_UNIT" --no-pager --lines=0 || true
echo
echo "Tips (instance '$INSTANCE'):"
echo "  # trigger a manual compile (exercises the path watcher):"
echo "  date -Is > $VAULT_REPO/inbox/.compile/request"
echo "  journalctl --user -u knowledge-compile@$INSTANCE.service -f"
echo "  # run synthesize / resolve on demand:"
echo "  systemctl --user start knowledge-synthesize@$INSTANCE.service   # journalctl --user -u knowledge-synthesize@$INSTANCE.service -f"
echo "  systemctl --user start knowledge-resolve@$INSTANCE.service      # journalctl --user -u knowledge-resolve@$INSTANCE.service -f"
echo "  # add another vault:"
echo "  KNOWLEDGE_INSTANCE=<name> KNOWLEDGE_REPO=/path/to/other-vault $SCRIPTS/install.sh"
