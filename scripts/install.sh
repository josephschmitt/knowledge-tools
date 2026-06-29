#!/usr/bin/env bash
# Install the vault automation as user-level scheduled jobs — for ONE vault per run.
#
# OS-conditional scheduler: systemd *user* units on Linux, launchd LaunchAgents on macOS. Same
# multi-vault model either way — each vault is an instance named by KNOWLEDGE_INSTANCE (default
# "default" for a single-vault host). Re-run this script once per vault to add another; the
# instances are independent (own config, own lock, own schedule) and run concurrently.
#
# Two repo paths are filled into the generated jobs (tooling and the vault live in separate repos):
#   - @TOOLS_REPO@ — this repo, where the worker scripts live (ExecStart / ProgramArguments).
#   - @VAULT_REPO@ — the vault repo, whose inbox is watched and whose library is operated on
#                    (from the required KNOWLEDGE_REPO).
#
# What gets generated per run (Linux / systemd):
#   - Shared service TEMPLATES (written once, reused by every instance):
#       knowledge-compile@.service / knowledge-synthesize@.service / knowledge-resolve@.service
#     Each reads this vault's KNOWLEDGE_REPO from %h/.config/knowledge-tools/%i.env.
#   - Per-vault units (THIS instance, carrying its schedule / watched path):
#       knowledge-compile@<inst>.timer + knowledge-compile@<inst>.path, plus the synthesize/resolve
#       timers.
#   - This vault's config file %h/.config/knowledge-tools/<inst>.env.
# What gets generated per run (macOS / launchd):
#   - Three concrete LaunchAgents per vault under ~/Library/LaunchAgents/:
#       com.knowledge-tools.{compile,synthesize,resolve}.<inst>.plist
#     The compile agent carries BOTH its schedule and the on-demand WatchPaths trigger (one launchd
#     label = one serialized job). Values are baked into each plist (launchd has no template/env-file
#     indirection); logs go to ~/Library/Logs/knowledge-tools/<inst>-<job>.log.
#
# synthesize/resolve run `gh` from inside their Claude run, so they need gh on PATH and a logged-in
# `gh auth` (~/.config/gh); the generated jobs set PATH and rely on HOME. Each vault's three jobs
# share one per-instance lockfile (see vault-lib.sh) so they never run concurrently — but different
# vaults DO run concurrently.
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

# The vault instance — one job set per vault. Default "default" so a single-vault host needs no
# extra config (and the existing user just re-runs install once to migrate). Restrict to a
# filename/systemd-safe slug so it needs no instance-name escaping.
INSTANCE="${KNOWLEDGE_INSTANCE:-default}"
case "$INSTANCE" in
  '' | *[!A-Za-z0-9_-]*)
    echo "error: KNOWLEDGE_INSTANCE='$INSTANCE' must be a non-empty slug of [A-Za-z0-9_-]." >&2
    exit 1
    ;;
esac

# Cadences — any systemd OnCalendar expression (e.g. hourly, daily, weekly, '*-*-* 03:00:00',
# '*-*-* *:00/30:00' for every 30 min; an optional trailing timezone like 'America/Detroit' is
# honored on systemd >= 250). On macOS a supported subset is translated to launchd and scheduled in
# LOCAL time (the trailing timezone is dropped) — see oncalendar_to_launchd. The compile worker
# no-ops on an empty inbox; resolve short-circuits with no open issues — so a frequent cadence for
# those only does real work when there's something to do. synthesize is heavy whole-corpus, so it
# defaults rare. Each vault keeps its own cadence (set these per run alongside KNOWLEDGE_INSTANCE).
#
# The two Claude-credit-hungry jobs (synthesize/resolve) default to the *middle of the night*
# (pinned to America/Detroit so it tracks local night through DST, even on a UTC host) to land
# on off-peak Max-plan capacity. They're staggered onto the half-hour and an hour apart so they
# don't collide with the hourly compile's top-of-hour fire window or each other — colliding jobs
# don't queue, they bail on the shared lock (and skip that tick).
ONCALENDAR="${KNOWLEDGE_COMPILE_ONCALENDAR:-hourly}"
SYNTH_ONCALENDAR="${KNOWLEDGE_SYNTHESIZE_ONCALENDAR:-Sun *-*-* 04:30:00 America/Detroit}"
RESOLVE_ONCALENDAR="${KNOWLEDGE_RESOLVE_ONCALENDAR:-*-*-* 03:30:00 America/Detroit}"

# Static site (Quartz) — OPT-IN, because building needs Node >= 20 (a host dependency the content
# jobs don't have). When enabled, install wires a standalone safety-net rebuild unit on this cadence
# AND the content jobs rebuild the site inline (see maybe_build_site in vault-lib.sh); when disabled
# (the default), neither happens and a re-run tears down any previously-installed site unit. The
# container serves the built tree separately via its own KNOWLEDGE_ENABLE_SITE — this host knob only
# controls *building*.
case "${KNOWLEDGE_SITE_ENABLED:-}" in
  1 | true | TRUE | yes) SITE_ENABLED=1 ;;
  *) SITE_ENABLED= ;;
esac
SITE_ONCALENDAR="${KNOWLEDGE_SITE_ONCALENDAR:-hourly}"

# synthesize/resolve run `gh` from inside their Claude run; warn (don't fail) if the host isn't set
# up for that, since compile doesn't need it and a fresh install may add gh later. OS-agnostic.
if ! command -v gh >/dev/null 2>&1; then
  echo "warning: 'gh' not found on PATH — synthesize/resolve can't file or close issues until" >&2
  echo "  the GitHub CLI is installed and on the generated job's PATH." >&2
elif ! gh auth status >/dev/null 2>&1; then
  echo "warning: 'gh' is not authenticated — run 'gh auth login' so synthesize/resolve can use it." >&2
fi

if [ ! -d "$VAULT_REPO" ]; then
  echo "warning: vault repo not found at $VAULT_REPO (from KNOWLEDGE_REPO)." >&2
fi

# ---------------------------------------------------------------------------------------------
# Linux: systemd user units (template instances). Unchanged from the single-OS install.
# ---------------------------------------------------------------------------------------------
install_systemd() {
  local UNIT_DIR CONFIG_DIR INSTANCE_ENV
  UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/knowledge-tools"
  INSTANCE_ENV="$CONFIG_DIR/$INSTANCE.env"

  # Shared service templates (instance-independent: KNOWLEDGE_REPO comes from the per-vault env file).
  local SERVICE_TEMPLATES=(knowledge-compile@.service knowledge-synthesize@.service knowledge-resolve@.service)
  # This vault's timers + compile path watcher (carry the schedule / watched path for this instance).
  local TIMERS=(
    "knowledge-compile@$INSTANCE.timer"
    "knowledge-synthesize@$INSTANCE.timer"
    "knowledge-resolve@$INSTANCE.timer"
  )
  local PATH_UNIT="knowledge-compile@$INSTANCE.path"
  # Static-site units (opt-in). The service template is shared across instances (like the others);
  # the timer is per-vault. Added to the render/enable lists only when enabled — otherwise a teardown
  # block below removes any that a prior enabled run left behind.
  local SITE_SERVICE="knowledge-site@.service"
  local SITE_TIMER="knowledge-site@$INSTANCE.timer"
  if [ -n "$SITE_ENABLED" ]; then
    SERVICE_TEMPLATES+=("$SITE_SERVICE")
    TIMERS+=("$SITE_TIMER")
  fi
  # Legacy single-vault units from before multi-vault — removed on migration so they don't double-fire.
  local LEGACY_UNITS=(
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
  [ -n "$SITE_ENABLED" ] && check_oncalendar KNOWLEDGE_SITE_ONCALENDAR "$SITE_ONCALENDAR"

  echo "Installing vault '$INSTANCE' via systemd (tools: $TOOLS_REPO, vault: $VAULT_REPO)"
  echo "  cadence: compile=$ONCALENDAR synthesize=$SYNTH_ONCALENDAR resolve=$RESOLVE_ONCALENDAR"
  [ -n "$SITE_ENABLED" ] && echo "  static site: enabled (rebuild cadence=$SITE_ONCALENDAR + inline after each content job)"
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
      # Static-site config — written only when enabled, so both the standalone unit AND the inline
      # build (compile/synthesize/resolve read this same env file) see it. Omitting them when
      # disabled is what makes maybe_build_site a no-op and toggles the feature off on re-run.
      if [ -n "$SITE_ENABLED" ]; then
        echo "KNOWLEDGE_SITE_ENABLED=1"
        [ -n "${KNOWLEDGE_SITE_ROOT:-}" ] && echo "KNOWLEDGE_SITE_ROOT=$KNOWLEDGE_SITE_ROOT"
        [ -n "${KNOWLEDGE_SITE_BASE_URL:-}" ] && echo "KNOWLEDGE_SITE_BASE_URL=$KNOWLEDGE_SITE_BASE_URL"
        [ -n "${KNOWLEDGE_SITE_TITLE:-}" ] && echo "KNOWLEDGE_SITE_TITLE=$KNOWLEDGE_SITE_TITLE"
        [ -n "${KNOWLEDGE_QUARTZ_REF:-}" ] && echo "KNOWLEDGE_QUARTZ_REF=$KNOWLEDGE_QUARTZ_REF"
        [ -n "${KNOWLEDGE_SITE_LOG_RETENTION_DAYS:-}" ] && echo "KNOWLEDGE_SITE_LOG_RETENTION_DAYS=$KNOWLEDGE_SITE_LOG_RETENTION_DAYS"
      fi
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
      -e "s|@SITE_ONCALENDAR@|$SITE_ONCALENDAR|g" \
      "$SCRIPTS/$1" >"$UNIT_DIR/$2"
    echo "  $2"
  }

  echo "Generating units in $UNIT_DIR"
  local t
  for t in "${SERVICE_TEMPLATES[@]}"; do
    render "$t.in" "$t"
  done
  render knowledge-compile@.timer.in    "knowledge-compile@$INSTANCE.timer"
  render knowledge-compile@.path.in     "$PATH_UNIT"
  render knowledge-synthesize@.timer.in "knowledge-synthesize@$INSTANCE.timer"
  render knowledge-resolve@.timer.in    "knowledge-resolve@$INSTANCE.timer"
  # The site service template renders via the SERVICE_TEMPLATES loop above (when enabled); its
  # per-vault timer is rendered here.
  [ -n "$SITE_ENABLED" ] && render knowledge-site@.timer.in "$SITE_TIMER"

  # Migrate a pre-multi-vault install: the old non-instanced units map to THIS run's @default units
  # (same vault, same schedule), so they'd double-fire. Only remove them when installing the default
  # instance — that's the run that writes the replacements. Installing a NON-default vault first (to
  # add a second vault before migrating the original) must leave the legacy units running until the
  # user re-runs for `default`; otherwise the original vault's automation would silently stop.
  if [ "$INSTANCE" = default ]; then
    local legacy_found=0 u
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

  # Site disabled: tear down any site units a prior enabled run installed, so toggling the feature
  # off cleans up after itself. The timer is per-vault (safe to remove); the service template is
  # SHARED across instances, so only remove it when no other instance's site timer still references
  # it. Harmless when nothing was there.
  if [ -z "$SITE_ENABLED" ] && { [ -e "$UNIT_DIR/$SITE_TIMER" ] || [ -e "$UNIT_DIR/$SITE_SERVICE" ]; }; then
    echo "Removing site units for '$INSTANCE' (KNOWLEDGE_SITE_ENABLED is off)"
    systemctl --user disable --now "$SITE_TIMER" "knowledge-site@$INSTANCE.service" 2>/dev/null || true
    rm -f "$UNIT_DIR/$SITE_TIMER"
    if ! ls "$UNIT_DIR"/knowledge-site@*.timer >/dev/null 2>&1; then
      rm -f "$UNIT_DIR/$SITE_SERVICE"
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

  # Seed the schedule snapshot (inbox/.compile/schedules.json) so the MCP/REST status surface can
  # report next-run times right away — before the first tick fires (matters for the weekly
  # synthesize). Runs in a subshell so vault-lib's helpers don't leak into install. Best-effort.
  ( . "$SCRIPTS/vault-lib.sh" && KNOWLEDGE_INSTANCE="$INSTANCE" refresh_schedules ) || true

  # Seed the first site build so it's published right away instead of waiting for the first tick.
  # --no-block so install doesn't hang on the first run's Quartz clone + npm ci (can be minutes).
  if [ -n "$SITE_ENABLED" ]; then
    echo "Kicking off the initial site build (best-effort, in the background)"
    systemctl --user start --no-block "knowledge-site@$INSTANCE.service" || \
      echo "  note: couldn't start it — check journalctl --user -u knowledge-site@$INSTANCE.service"
  fi
  echo
  echo "Tips (instance '$INSTANCE'):"
  echo "  # trigger a manual compile (exercises the path watcher):"
  echo "  date -Is > $VAULT_REPO/inbox/.compile/request"
  echo "  journalctl --user -u knowledge-compile@$INSTANCE.service -f"
  echo "  # run synthesize / resolve on demand:"
  echo "  systemctl --user start knowledge-synthesize@$INSTANCE.service   # journalctl --user -u knowledge-synthesize@$INSTANCE.service -f"
  echo "  systemctl --user start knowledge-resolve@$INSTANCE.service      # journalctl --user -u knowledge-resolve@$INSTANCE.service -f"
  if [ -n "$SITE_ENABLED" ]; then
    echo "  # rebuild the static site on demand:"
    echo "  systemctl --user start knowledge-site@$INSTANCE.service         # journalctl --user -u knowledge-site@$INSTANCE.service -f"
  fi
  echo "  # add another vault:"
  echo "  KNOWLEDGE_INSTANCE=<name> KNOWLEDGE_REPO=/path/to/other-vault $SCRIPTS/install.sh"
}

# ---------------------------------------------------------------------------------------------
# macOS: launchd LaunchAgents. systemd has no analog here, so we translate the same cadence knobs
# into launchd's far smaller scheduling vocabulary and bake one concrete plist per vault.
# ---------------------------------------------------------------------------------------------

# Replace a whole placeholder line (@KEY@, alone on its line) with a possibly-multi-line value.
# sed can't cleanly splice multi-line content, so use awk. The value goes through the environment
# (ENVIRON[]) rather than -v: macOS's BSD awk rejects a newline in a -v assignment. Reads stdin.
inject() { # <placeholder> <multiline-value>
  INJECT_VAL="$2" awk -v key="$1" 'index($0, key) { print ENVIRON["INJECT_VAL"]; next } { print }'
}

# XML-escape stdin (&, <, >) for embedding in a plist <string>. '&' first, or it would re-escape
# the '&' the </>/ rules introduce.
_xml_escape() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# Make a value safe to substitute into a plist <string> via `sed -e "s|@TOK@|VALUE|g"`: XML-escape
# it, then escape the chars sed treats specially in a REPLACEMENT (\, &, and our | delimiter) so a
# path like /Users/a&b or one containing | lands literally instead of corrupting the plist.
_plist_val() { # <value> -> escaped string for use as a sed replacement
  printf '%s' "$1" | _xml_escape | sed -e 's/[\\&|]/\\&/g'
}

# Emit a <key>StartCalendarInterval</key><dict>…</dict> block. Args: Key Int [Key Int ...].
# Indented to sit at the plist's top-level dict (2 spaces), inner keys at 4.
_cal() {
  printf '  <key>StartCalendarInterval</key>\n  <dict>\n'
  while [ "$#" -ge 2 ]; do
    printf '    <key>%s</key><integer>%s</integer>\n' "$1" "$2"
    shift 2
  done
  printf '  </dict>'
}
_interval() { printf '  <key>StartInterval</key><integer>%s</integer>' "$1"; }  # <seconds>

_dow_num() { # systemd weekday name -> launchd Weekday integer (Sun=0, Mon=1 … Sat=6)
  case "$1" in
    [Mm]on*) echo 1 ;; [Tt]ue*) echo 2 ;; [Ww]ed*) echo 3 ;; [Tt]hu*) echo 4 ;;
    [Ff]ri*) echo 5 ;; [Ss]at*) echo 6 ;; [Ss]un*) echo 0 ;; *) echo "" ;;
  esac
}

# Translate an OnCalendar-ish value to a launchd schedule fragment (printed to stdout), or return 1
# (with a message on stderr) if it's outside the supported macOS subset. The trailing timezone is
# stripped — launchd schedules in the machine's local time.
oncalendar_to_launchd() { # <value>
  local v="$1" dow="" timepart hh rest mm step off
  # Drop a trailing timezone token (launchd schedules in local time). A tz name starts with a
  # letter (America/Detroit, Europe/London, UTC); the time field starts with a digit or '*', and a
  # leading weekday has no preceding space — so a " <letter>" only ever matches the trailing tz.
  case "$v" in *' '[A-Za-z]*) v="${v% *}" ;; esac
  v="${v%"${v##*[![:space:]]}"}"                              # rstrip

  case "$v" in
    hourly) _cal Minute 0; return 0 ;;
    daily)  _cal Hour 0 Minute 0; return 0 ;;
    weekly) _cal Weekday 1 Hour 0 Minute 0; return 0 ;;       # systemd weekly = Mon 00:00
  esac

  # Optional leading weekday (e.g. "Sun *-*-* 04:30:00").
  case "$v" in
    [A-Za-z]*\ *) dow="$(_dow_num "${v%% *}")"; v="${v#* }"
      [ -n "$dow" ] || { _unsupported "$1"; return 1; } ;;
  esac
  # Only the all-dates glob is supported; the time follows it.
  case "$v" in
    '*-*-* '*) timepart="${v#\*-\*-\* }" ;;
    *) _unsupported "$1"; return 1 ;;
  esac

  hh="${timepart%%:*}"; rest="${timepart#*:}"; mm="${rest%%:*}"
  if [ "$hh" = '*' ]; then
    case "$mm" in
      */*) off="${mm%%/*}"; step="${mm#*/}"; case "$step" in ''|*[!0-9]*) _unsupported "$1"; return 1 ;; esac
           # step must be a positive count of minutes — 0 would mean StartInterval 0, which launchd
           # treats as "fire continuously" (a tight compile loop). 10# avoids an octal parse on '08'.
           [ "$((10#$step))" -gt 0 ] || { _unsupported "$1"; return 1; }
           # launchd StartInterval counts from agent load, so it can't honor a systemd start offset
           # (the ':08' in *:08/30). Warn to STDERR (stdout is the captured schedule fragment) so a
           # deliberately-staggered cadence isn't silently un-staggered on macOS.
           case "$off" in ''|*[!0-9]*) ;; *) [ "$((10#$off))" -eq 0 ] || \
             echo "warning: cadence '$1': launchd ignores the ':$off' start offset — StartInterval fires every $((10#$step)) min from load, not aligned to :$off." >&2 ;; esac
           _interval "$(( (10#$step) * 60 ))"; return 0 ;;     # every-N-min → StartInterval
      *[!0-9]*|'') _unsupported "$1"; return 1 ;;
      *) [ "$((10#$mm))" -le 59 ] || { _unsupported "$1"; return 1; }
         _cal Minute "$((10#$mm))"; return 0 ;;               # hourly at minute MM
    esac
  fi
  case "$hh$mm" in *[!0-9]*|'') _unsupported "$1"; return 1 ;; esac
  # Reject out-of-range times here so the script fails clearly at cadence validation, rather than
  # emitting Hour=25/Minute=99 that launchd silently rejects at bootstrap (agent not installed).
  [ "$((10#$hh))" -le 23 ] && [ "$((10#$mm))" -le 59 ] || { _unsupported "$1"; return 1; }
  if [ -n "$dow" ]; then
    _cal Weekday "$dow" Hour "$((10#$hh))" Minute "$((10#$mm))"
  else
    _cal Hour "$((10#$hh))" Minute "$((10#$mm))"
  fi
}
_unsupported() {
  echo "error: cadence '$1' isn't supported on macOS (launchd)." >&2
  echo "  Supported: hourly | daily | weekly | '[Dow ]*-*-* HH:MM:SS' | '*-*-* *:MM/STEP:SS' (every STEP min)." >&2
}

install_launchd() {
  local LA_DIR LOG_DIR_M LA_PATH ENV_EXTRA uid
  LA_DIR="$HOME/Library/LaunchAgents"
  LOG_DIR_M="$HOME/Library/Logs/knowledge-tools"
  # launchd hands jobs a bare PATH; prepend where claude + gh actually live (Homebrew on both
  # Apple-Silicon and Intel, plus the local/nix profiles) so the workers and `gh` resolve.
  LA_PATH="/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$HOME/.nix-profile/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  uid="$(id -u)"

  # Optional per-vault vars — the macOS analog of what the systemd path writes into <inst>.env.
  # launchd can't read an EnvironmentFile, so bake them straight into EnvironmentVariables.
  ENV_EXTRA=""
  _env_kv() { # <name> <value>  -> append an indented <key>/<string> pair to ENV_EXTRA
    # XML-escape the value so a '&', '<', or '>' can't produce a malformed plist that fails
    # plutil/bootstrap silently. (No sed-replacement escaping needed: this goes into ENV_EXTRA,
    # which inject() splices via awk, not sed.)
    local v; v="$(printf '%s' "$2" | _xml_escape)"
    ENV_EXTRA="$ENV_EXTRA    <key>$1</key>
    <string>$v</string>
"
  }
  [ -n "${KNOWLEDGE_REVIEW_CHANNEL:-}" ] && _env_kv KNOWLEDGE_REVIEW_CHANNEL "$KNOWLEDGE_REVIEW_CHANNEL"
  [ -n "${KNOWLEDGE_GITHUB_REPO:-}" ] && _env_kv KNOWLEDGE_GITHUB_REPO "$KNOWLEDGE_GITHUB_REPO"
  [ -n "${KNOWLEDGE_COMPILE_COOLDOWN:-}" ] && _env_kv KNOWLEDGE_COMPILE_COOLDOWN "$KNOWLEDGE_COMPILE_COOLDOWN"
  # Static-site config baked into EVERY agent's environment: the standalone site agent needs it to
  # build, and the compile/synthesize/resolve agents need KNOWLEDGE_SITE_ENABLED to fire their
  # inline maybe_build_site. Written only when enabled (launchd has no EnvironmentFile, so this is
  # the macOS analog of the systemd <inst>.env site block).
  if [ -n "$SITE_ENABLED" ]; then
    _env_kv KNOWLEDGE_SITE_ENABLED 1
    [ -n "${KNOWLEDGE_SITE_ROOT:-}" ] && _env_kv KNOWLEDGE_SITE_ROOT "$KNOWLEDGE_SITE_ROOT"
    [ -n "${KNOWLEDGE_SITE_BASE_URL:-}" ] && _env_kv KNOWLEDGE_SITE_BASE_URL "$KNOWLEDGE_SITE_BASE_URL"
    [ -n "${KNOWLEDGE_SITE_TITLE:-}" ] && _env_kv KNOWLEDGE_SITE_TITLE "$KNOWLEDGE_SITE_TITLE"
    [ -n "${KNOWLEDGE_QUARTZ_REF:-}" ] && _env_kv KNOWLEDGE_QUARTZ_REF "$KNOWLEDGE_QUARTZ_REF"
    [ -n "${KNOWLEDGE_SITE_LOG_RETENTION_DAYS:-}" ] && _env_kv KNOWLEDGE_SITE_LOG_RETENTION_DAYS "$KNOWLEDGE_SITE_LOG_RETENTION_DAYS"
  fi
  ENV_EXTRA="${ENV_EXTRA%$'\n'}"   # drop the trailing newline so the placeholder line is replaced cleanly

  # Validate + translate all three cadences up front (fail before writing any plist).
  local sched_compile sched_synth sched_resolve sched_site=""
  sched_compile="$(oncalendar_to_launchd "$ONCALENDAR")" || exit 1
  sched_synth="$(oncalendar_to_launchd "$SYNTH_ONCALENDAR")" || exit 1
  sched_resolve="$(oncalendar_to_launchd "$RESOLVE_ONCALENDAR")" || exit 1
  [ -n "$SITE_ENABLED" ] && { sched_site="$(oncalendar_to_launchd "$SITE_ONCALENDAR")" || exit 1; }

  echo "Installing vault '$INSTANCE' via launchd (tools: $TOOLS_REPO, vault: $VAULT_REPO)"
  echo "  cadence: compile=$ONCALENDAR synthesize=$SYNTH_ONCALENDAR resolve=$RESOLVE_ONCALENDAR (local time)"
  [ -n "$SITE_ENABLED" ] && echo "  static site: enabled (rebuild cadence=$SITE_ONCALENDAR + inline after each content job)"
  mkdir -p "$LA_DIR" "$LOG_DIR_M" "$VAULT_REPO/inbox/.compile"
  # The compile agent's WatchPaths watches the request FILE; if it's absent launchd falls back to
  # watching the parent dir and the worker's own status.json writes would loop it. Create the file
  # so the watch targets it from the start. Leave an existing one (and its mtime, which may flag a
  # pending request) untouched — vault-compile.sh keeps this file permanent on macOS.
  [ -e "$VAULT_REPO/inbox/.compile/request" ] || : >"$VAULT_REPO/inbox/.compile/request"

  gen_plist() { # <template basename> <job> <schedule-fragment>
    local tmpl="$SCRIPTS/$1" job="$2" sched="$3"
    local label="com.knowledge-tools.$job.$INSTANCE"
    local dest="$LA_DIR/$label.plist"
    local log="$LOG_DIR_M/$INSTANCE-$job.log"
    # The path-ish values land inside plist <string> elements, so escape each for XML + sed (a '&'
    # or '|' in $HOME / the repo path would otherwise corrupt the plist or break the substitution).
    # INSTANCE/LABEL are a validated [A-Za-z0-9_-] slug, so they need none.
    local tools_v vault_v path_v log_v
    tools_v="$(_plist_val "$TOOLS_REPO")"; vault_v="$(_plist_val "$VAULT_REPO")"
    path_v="$(_plist_val "$LA_PATH")";     log_v="$(_plist_val "$log")"
    sed -e "s|@TOOLS_REPO@|$tools_v|g" -e "s|@VAULT_REPO@|$vault_v|g" \
      -e "s|@INSTANCE@|$INSTANCE|g" -e "s|@LABEL@|$label|g" \
      -e "s|@PATH@|$path_v|g" -e "s|@LOG@|$log_v|g" \
      "$tmpl" \
      | inject '@SCHEDULE@' "$sched" \
      | inject '@ENV_EXTRA@' "$ENV_EXTRA" \
      >"$dest"
    echo "  $dest"
    # Idempotent (re)load: bootout the old instance if present, then bootstrap the fresh plist.
    # Fall back to the legacy load verbs on macOS too old for bootstrap/bootout. Capture bootstrap's
    # error so it can be shown only if BOTH paths fail — at which point bootout has already unloaded
    # any previously-running agent, so the job is now DEAD and we must fail loudly, not report success.
    launchctl bootout "gui/$uid/$label" 2>/dev/null || true
    local err
    if err="$(launchctl bootstrap "gui/$uid" "$dest" 2>&1)"; then return 0; fi
    launchctl unload "$dest" 2>/dev/null || true
    if launchctl load -w "$dest" 2>/dev/null; then return 0; fi
    echo "error: could not load $label — the agent is NOT running (any previous one was already" >&2
    echo "  unloaded). Fix the cause and re-run. launchctl bootstrap said: ${err:-<no output>}" >&2
    return 1
  }

  echo "Generating LaunchAgents in $LA_DIR"
  gen_plist knowledge-compile.plist.in    compile    "$sched_compile" || exit 1
  gen_plist knowledge-synthesize.plist.in synthesize "$sched_synth"   || exit 1
  gen_plist knowledge-resolve.plist.in    resolve    "$sched_resolve" || exit 1
  if [ -n "$SITE_ENABLED" ]; then
    gen_plist knowledge-site.plist.in     site       "$sched_site"    || exit 1
    # gen_plist bootstraps with RunAtLoad=false, so seed the first build explicitly (best-effort) so
    # the site is published right away rather than waiting for the first tick.
    launchctl kickstart -k "gui/$uid/com.knowledge-tools.site.$INSTANCE" 2>/dev/null || true
  else
    # Site disabled: tear down a site agent a prior enabled run installed, so toggling off cleans up.
    local site_label="com.knowledge-tools.site.$INSTANCE"
    if [ -e "$LA_DIR/$site_label.plist" ]; then
      echo "Removing site agent (KNOWLEDGE_SITE_ENABLED is off): $LA_DIR/$site_label.plist"
      launchctl bootout "gui/$uid/$site_label" 2>/dev/null || true
      rm -f "$LA_DIR/$site_label.plist"
    fi
  fi

  echo
  echo "Done. Loaded agents:"
  launchctl list 2>/dev/null | grep "knowledge-tools.*\.$INSTANCE\$" || echo "  (none reported — check 'launchctl list | grep knowledge')"
  echo
  echo "note: LaunchAgents run while you're LOGGED IN — there's no linger equivalent. A scheduled"
  echo "  night job needs the Mac powered on and logged in (or awake via 'pmset') at that time."
  echo
  echo "Tips (instance '$INSTANCE'):"
  echo "  # trigger a manual compile (exercises the WatchPaths trigger):"
  echo "  date +%s > $VAULT_REPO/inbox/.compile/request"
  echo "  tail -f $LOG_DIR_M/$INSTANCE-compile.log"
  echo "  # run synthesize / resolve on demand:"
  echo "  launchctl kickstart -k gui/$uid/com.knowledge-tools.synthesize.$INSTANCE   # tail -f $LOG_DIR_M/$INSTANCE-synthesize.log"
  echo "  launchctl kickstart -k gui/$uid/com.knowledge-tools.resolve.$INSTANCE      # tail -f $LOG_DIR_M/$INSTANCE-resolve.log"
  if [ -n "$SITE_ENABLED" ]; then
    echo "  # rebuild the static site on demand:"
    echo "  launchctl kickstart -k gui/$uid/com.knowledge-tools.site.$INSTANCE        # tail -f $LOG_DIR_M/$INSTANCE-site.log"
  fi
  echo "  # add another vault:"
  echo "  KNOWLEDGE_INSTANCE=<name> KNOWLEDGE_REPO=/path/to/other-vault $SCRIPTS/install.sh"
}

OS="$(uname -s)"
case "$OS" in
  Linux)  install_systemd ;;
  Darwin) install_launchd ;;
  *) echo "error: unsupported OS '$OS' — need Linux (systemd) or macOS (launchd)." >&2; exit 1 ;;
esac
