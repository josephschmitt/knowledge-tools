#!/usr/bin/env bash
# Sourced by the host compile scripts (install.sh, vault-compile.sh) to load config
# like KNOWLEDGE_REPO from the repo-root .env. Override the path with KNOWLEDGE_ENV_FILE.
#
# Values already set in the environment WIN — an explicit `KNOWLEDGE_REPO=... ./install.sh`
# or a systemd `Environment=` is never overridden by .env. Lines are literal KEY=value with
# no shell expansion, so write absolute paths (not ~ or $HOME). Blank lines and #comments
# are ignored; one layer of surrounding quotes is stripped from values.

__env_lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${KNOWLEDGE_ENV_FILE:-$__env_lib_dir/../.env}"

if [ -f "$ENV_FILE" ]; then
  while IFS= read -r __line || [ -n "$__line" ]; do
    case "$__line" in '' | '#'*) continue ;; esac
    __key="${__line%%=*}"
    __val="${__line#*=}"
    # trim surrounding whitespace from the key
    __key="${__key#"${__key%%[![:space:]]*}"}"
    __key="${__key%"${__key##*[![:space:]]}"}"
    [ -n "$__key" ] || continue
    # strip one layer of matching quotes from the value
    case "$__val" in
      \"*\") __val="${__val#\"}"; __val="${__val%\"}" ;;
      \'*\') __val="${__val#\'}"; __val="${__val%\'}" ;;
    esac
    if [ -z "${!__key+x}" ]; then
      export "$__key=$__val"
    fi
  done <"$ENV_FILE"
  unset __line __key __val
fi
unset __env_lib_dir
