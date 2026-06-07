#!/usr/bin/env bash
# Seed a fresh knowledge vault from template/ — a ONE-SHOT scaffold, not a sync.
#
# This is deliberately NOT install.sh. install.sh re-generates host-side systemd units and
# is meant to be re-run whenever a template or cadence changes. This script only lays down
# the *starting point* of a vault's own librarian (CLAUDE.md, the /compile-inbox /synthesize
# /resolve commands, the folder skeleton, empty index.md/log.md) into KNOWLEDGE_REPO.
#
# After seeding, those files belong to the vault and are expected to DRIFT: the librarian is
# content-coupled and gets tuned as the corpus grows. That divergence is the design, not a
# bug to reconcile — so this script is strictly copy-if-absent:
#   - Files that already exist in the vault are never touched. A tuned compile-inbox.md or a
#     grown CLAUDE.md is safe; re-running only fills in what's MISSING.
#   - There is no --force. "Reset this one file to the seed" is: delete it, then re-run.
#
# Usage:
#   scripts/init-vault.sh [VAULT_DIR]
# VAULT_DIR defaults to KNOWLEDGE_REPO (from the repo-root .env or the environment).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="$SCRIPT_DIR/../template"

# Config: real env wins over .env; an explicit VAULT_DIR arg wins over both.
. "$SCRIPT_DIR/load-env.sh"
VAULT="${1:-${KNOWLEDGE_REPO:-}}"
if [ -z "$VAULT" ]; then
  echo "init-vault.sh: no target — pass a VAULT_DIR or set KNOWLEDGE_REPO (.env or environment)" >&2
  exit 1
fi
if [ ! -d "$TEMPLATE_DIR" ]; then
  echo "init-vault.sh: template dir not found at $TEMPLATE_DIR" >&2
  exit 1
fi

mkdir -p "$VAULT"
echo "Seeding vault at: $VAULT"
echo "From template:    $TEMPLATE_DIR"

created=0 skipped=0
while IFS= read -r src; do
  rel="${src#"$TEMPLATE_DIR"/}"
  dest="$VAULT/$rel"
  base="$(basename "$rel")"

  # A .gitkeep only exists to carry an otherwise-empty dir. Don't plant one in a directory
  # that already has real content (e.g. a grown wiki/) — the dir no longer needs keeping.
  if [ "$base" = ".gitkeep" ]; then
    parent="$(dirname "$dest")"
    if [ -d "$parent" ] && [ -n "$(ls -A "$parent" 2>/dev/null)" ]; then
      skipped=$((skipped + 1)); continue
    fi
  fi

  if [ -e "$dest" ]; then
    echo "  skip   $rel (exists)"
    skipped=$((skipped + 1))
    continue
  fi

  mkdir -p "$(dirname "$dest")"
  cp "$src" "$dest"
  echo "  create $rel"
  created=$((created + 1))
done < <(find "$TEMPLATE_DIR" -type f | sort)

echo "Done: $created created, $skipped left untouched."
if [ "$created" -gt 0 ]; then
  echo "Next: cd \"$VAULT\" && git init (if needed) and make the first commit yourself —"
  echo "init-vault.sh leaves git alone."
fi
