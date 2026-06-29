#!/usr/bin/env bash
# Stage the vault's PUBLIC content (allowlist) and build the Quartz site, publishing atomically.
#
# Ported from the retired scripts/vault-site.sh (recoverable from git history at e9ed2ae): the
# privacy boundary is the staging allowlist — only index.md + library/ from VAULT_ROOT ever reach
# the build; inbox/, outputs/, tasks/ never do. Read-only w.r.t. the bind-mounted vault; writes
# only under /srv. Quartz config + node_modules are baked into the image at /opt/quartz.
#
# Callable repeatedly (initial build + every POST /rebuild). The atomic swap means a concurrent
# reader never sees a half-built tree.
set -euo pipefail

VAULT_ROOT="${VAULT_ROOT:-/vault}"
QUARTZ_DIR="/opt/quartz"
STAGE="/srv/stage"
SITE_OUT="/srv/site"

# ISO-8601-ish, portable across GNU (container) and BSD/macOS date (local testing) — `date -Is`
# isn't accepted by BSD date.
log() { printf '%s site: %s\n' "$(date +%Y-%m-%dT%H:%M:%S%z)" "$*"; }

# --- Stage content (ALLOWLIST — privacy boundary). Only index.md + library/ ever reach the site. ---
mkdir -p "$STAGE"
SOURCES=()
[ -f "$VAULT_ROOT/index.md" ] && SOURCES+=("$VAULT_ROOT/index.md")
[ -d "$VAULT_ROOT/library" ] && SOURCES+=("$VAULT_ROOT/library")
if [ "${#SOURCES[@]}" -eq 0 ]; then
  log "ERROR: no content to publish — neither $VAULT_ROOT/index.md nor $VAULT_ROOT/library exists." >&2
  exit 1
fi
# rsync --delete prunes notes removed from within a synced dir, but only INSIDE the sources it's
# given — a source that vanished entirely leaves a stale staged copy, so drop those explicitly.
[ ! -f "$VAULT_ROOT/index.md" ] && rm -f "$STAGE/index.md"
[ ! -d "$VAULT_ROOT/library" ] && rm -rf "$STAGE/library"
rsync -a --delete "${SOURCES[@]}" "$STAGE/"
log "staged $(find "$STAGE" -name '*.md' | wc -l | tr -d ' ') markdown file(s)"

# --- Build, then publish atomically (the server never serves a half-built tree). ---
rm -rf "$SITE_OUT.tmp"
log "running quartz build"
( cd "$QUARTZ_DIR" && npx quartz build -d "$STAGE" -o "$SITE_OUT.tmp" )

rm -rf "$SITE_OUT.prev"
[ -e "$SITE_OUT" ] && mv "$SITE_OUT" "$SITE_OUT.prev"
mv "$SITE_OUT.tmp" "$SITE_OUT"
rm -rf "$SITE_OUT.prev"
log "published site to $SITE_OUT ($(find "$SITE_OUT" -name '*.html' | wc -l | tr -d ' ') pages)."
