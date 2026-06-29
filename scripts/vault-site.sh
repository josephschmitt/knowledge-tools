#!/usr/bin/env bash
# Build the static Quartz site for one vault and publish it where the service serves it at /.
#
# Quartz is a clone-and-customize generator (NOT an npm dep), so this script maintains a pinned
# upstream checkout in a host state dir, overlays this repo's config (site/quartz.config.ts +
# quartz.layout.ts), STAGES a privacy-safe copy of the vault content (only index.md + library/ —
# never inbox/, outputs/, tasks/, logs), runs `quartz build`, and atomically publishes the output
# OUTSIDE the vault (so compile's `git add -A` never sweeps it up; the service bind-mounts it).
#
# Read-only w.r.t. the vault — it makes no commits and needs no git. Run two ways:
#   - standalone, via knowledge-site@<inst>.service/.timer: takes the shared lock, hard-fails on
#     error (systemd flags the unit).
#   - inline at the end of a content job (compile/synthesize/resolve): pass `--no-lock` (the caller
#     already holds the per-instance lock) and `--soft` (a Quartz hiccup must not fail the compile).
#
# Flags:
#   --no-lock  skip acquire_vault_lock (caller already holds it — avoids deadlocking the shared fd)
#   --soft     exit 0 even if the build fails (for inline use; logs the failure)
set -euo pipefail

# Shared config (REPO, the per-instance lock keyed by KNOWLEDGE_INSTANCE) + helpers. Sourcing this
# also loads the repo .env and requires KNOWLEDGE_REPO. We use only REPO + acquire_vault_lock here
# (no sync/commit — the site is a derived artifact, not vault content).
SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLS_REPO="$(cd "$SCRIPTS/.." && pwd)"
. "$SCRIPTS/vault-lib.sh"

NO_LOCK=
SOFT=
for arg in "$@"; do
  case "$arg" in
    --no-lock) NO_LOCK=1 ;;
    --soft) SOFT=1 ;;
    *) echo "vault-site.sh: unknown argument '$arg'" >&2; exit 2 ;;
  esac
done

INSTANCE="${KNOWLEDGE_INSTANCE:-default}"
STATE="${XDG_STATE_HOME:-$HOME/.local/state}/knowledge-tools"

# Pinned Quartz checkout — SHARED across vaults (config is overlaid per build, so one checkout +
# one node_modules serves every instance). Bump the ref by setting KNOWLEDGE_QUARTZ_REF (and update
# site/quartz.{config,layout}.ts to match — see site/README.md). Pinned to v4; v5 is a port, not a bump.
QUARTZ_REF="${KNOWLEDGE_QUARTZ_REF:-v4.5.2}"
QUARTZ_DIR="${KNOWLEDGE_QUARTZ_DIR:-$STATE/quartz}"
QUARTZ_URL="${KNOWLEDGE_QUARTZ_URL:-https://github.com/jackyzha0/quartz}"

# Per-instance staging + output (the output is what the container bind-mounts at SITE_ROOT).
STAGE="${KNOWLEDGE_SITE_STAGE:-$STATE/site-stage/$INSTANCE}"
SITE_OUT="${KNOWLEDGE_SITE_ROOT:-$STATE/site/$INSTANCE}"

LOG_DIR="$STATE/site-logs/$INSTANCE"
mkdir -p "$LOG_DIR"
# One log per run, and this runs inline after every (hourly) compile, so prune old logs to cap
# growth. Keep ~30 days; tune with KNOWLEDGE_SITE_LOG_RETENTION_DAYS.
find "$LOG_DIR" -type f -name '*.log' -mtime "+${KNOWLEDGE_SITE_LOG_RETENTION_DAYS:-30}" -delete 2>/dev/null || true
STAMP="$(date +%Y-%m-%d_%H%M%S)"
LOG="$LOG_DIR/$STAMP.log"
log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG"; }

# Fail (or soft-fail) with a message. Honors --soft so an inline call never fails the content job.
fail() {
  log "ERROR: $*"
  if [ -n "$SOFT" ]; then
    log "(--soft) leaving the previously published site in place."
    exit 0
  fi
  exit 1
}

log "building site for instance '$INSTANCE' (vault: $REPO, ref: $QUARTZ_REF)"

# --- Preflight: Node (Quartz v4 needs >= 20). A hard new host dependency the other jobs don't have. ---
if ! command -v node >/dev/null 2>&1; then
  fail "node not found on PATH — Quartz needs Node >= 20. Install it (the service units add ~/.nix-profile/bin)."
fi
NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"
if [ "$NODE_MAJOR" -lt 20 ]; then
  fail "node $(node -v) is too old — Quartz needs Node >= 20."
fi

# Take the shared per-instance lock so we read a consistent library snapshot and never race a compile.
# Skipped with --no-lock (the caller already holds it; re-flocking the inherited fd would deadlock).
if [ -z "$NO_LOCK" ]; then
  acquire_vault_lock
fi

# --- Maintain the pinned Quartz checkout (clone once, then fetch+checkout the pinned ref). ---
if [ ! -d "$QUARTZ_DIR/.git" ]; then
  log "cloning Quartz $QUARTZ_REF into $QUARTZ_DIR"
  rm -rf "$QUARTZ_DIR"
  git clone --quiet --depth 1 --branch "$QUARTZ_REF" "$QUARTZ_URL" "$QUARTZ_DIR" >>"$LOG" 2>&1 \
    || fail "git clone of Quartz $QUARTZ_REF failed."
else
  log "updating Quartz checkout to $QUARTZ_REF"
  git -C "$QUARTZ_DIR" fetch --quiet --depth 1 origin "refs/tags/$QUARTZ_REF:refs/tags/$QUARTZ_REF" >>"$LOG" 2>&1 \
    || git -C "$QUARTZ_DIR" fetch --quiet --depth 1 origin "$QUARTZ_REF" >>"$LOG" 2>&1 \
    || fail "git fetch of Quartz $QUARTZ_REF failed."
  git -C "$QUARTZ_DIR" checkout --quiet --force "$QUARTZ_REF" >>"$LOG" 2>&1 \
    || fail "git checkout of Quartz $QUARTZ_REF failed."
fi

# Install deps only when the checked-out ref changes (or node_modules is missing) — npm ci is slow.
STAMP_FILE="$QUARTZ_DIR/.knowledge-tools-ref"
if [ ! -d "$QUARTZ_DIR/node_modules" ] || [ "$(cat "$STAMP_FILE" 2>/dev/null || true)" != "$QUARTZ_REF" ]; then
  log "installing Quartz dependencies (npm ci) — this can take a while on first run"
  ( cd "$QUARTZ_DIR" && npm ci ) >>"$LOG" 2>&1 || fail "npm ci in $QUARTZ_DIR failed."
  printf '%s' "$QUARTZ_REF" >"$STAMP_FILE"
fi

# --- Overlay this repo's config onto the checkout. ---
for f in quartz.config.ts quartz.layout.ts; do
  cp "$TOOLS_REPO/site/$f" "$QUARTZ_DIR/$f" || fail "could not copy site/$f into the checkout."
done

# --- Stage content (ALLOWLIST — privacy boundary). Only index.md + library/ ever reach the site. ---
mkdir -p "$STAGE"
SOURCES=()
[ -f "$REPO/index.md" ] && SOURCES+=("$REPO/index.md")
[ -d "$REPO/library" ] && SOURCES+=("$REPO/library")
if [ "${#SOURCES[@]}" -eq 0 ]; then
  fail "no content to publish — neither $REPO/index.md nor $REPO/library exists."
fi
if command -v rsync >/dev/null 2>&1; then
  # --delete prunes notes removed from the vault within synced directories; only deltas copy, so
  # this is cheap to re-run. But rsync --delete only prunes INSIDE the sources it's syncing, so a
  # source that's been dropped entirely (its vault path no longer exists) leaves its stale staged
  # copy behind — explicitly remove it so deleted content stops being published.
  [ ! -f "$REPO/index.md" ] && rm -f "$STAGE/index.md"
  [ ! -d "$REPO/library" ] && rm -rf "$STAGE/library"
  rsync -a --delete "${SOURCES[@]}" "$STAGE/" >>"$LOG" 2>&1 || fail "staging (rsync) failed."
else
  rm -rf "${STAGE:?}/"* && cp -a "${SOURCES[@]}" "$STAGE/" || fail "staging (cp) failed."
fi
log "staged $(find "$STAGE" -name '*.md' | wc -l | tr -d ' ') markdown file(s)"

# --- Build, then publish atomically (the container never serves a half-built tree). ---
rm -rf "$SITE_OUT.tmp"
log "running quartz build"
if ! ( cd "$QUARTZ_DIR" && npx quartz build -d "$STAGE" -o "$SITE_OUT.tmp" ) >>"$LOG" 2>&1; then
  rm -rf "$SITE_OUT.tmp"
  fail "quartz build failed — see $LOG."
fi

mkdir -p "$(dirname "$SITE_OUT")"
rm -rf "$SITE_OUT.prev"
[ -e "$SITE_OUT" ] && mv "$SITE_OUT" "$SITE_OUT.prev"
mv "$SITE_OUT.tmp" "$SITE_OUT"
rm -rf "$SITE_OUT.prev"

log "published site to $SITE_OUT ($(find "$SITE_OUT" -name '*.html' | wc -l | tr -d ' ') pages)."
log "done."
