# knowledge-tools

Infrastructure for a personal "LLM wiki" — a knowledge base where raw captures land in
`inbox/` and Claude Code compiles them into durable, cross-linked notes in `wiki/`,
following Andrej Karpathy's [LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)
pattern: immutable raw sources, an LLM-owned wiki of markdown files, and a schema document
(`CLAUDE.md`) that defines the workflows. This repo holds everything that operates *on* the
vault from the outside; the vault itself — the notes plus the `CLAUDE.md` librarian spec and
`/compile-inbox` command Claude runs inside it — lives in a separate, private repo.

## Components

- **`mcp/`** — the claude.ai connector. A Streamable-HTTP MCP server (OAuth 2.1 resource
  server, gated by Cloudflare Access OIDC) that lets claude.ai capture raw material into the
  vault's `inbox/` and query the compiled `wiki/`. Reads/writes the vault via the `VAULT_ROOT`
  env var (bind-mounted into the container). Deployed separately — see [`mcp/README.md`](mcp/README.md).
- **`skills/knowledge-vault/`** — the conversational front-door skill for claude.ai: capture
  and query, delegating heavy compilation to the host. Packaged into release zips by CI.
- **`scripts/`** — the host-side compile automation. `nightly-compile.sh` runs an ephemeral
  Claude Code pass (`/compile-inbox`) over the vault; `install.sh` generates the systemd
  *user* units from the `knowledge-compile.*.in` templates; `validate_skills.py` is used by CI.

## Compile automation (host setup)

Compiling the inbox into the wiki runs on the host as systemd *user* units:

- `knowledge-compile.service` — one ephemeral inbox→wiki compile (the worker).
- `knowledge-compile.timer` — runs it nightly at 03:00.
- `knowledge-compile.path` — runs it on demand when the MCP server's `compile_run` tool
  drops `inbox/.compile/request` into the vault. Both triggers start the same service, so
  systemd runs only one compile at a time (the lock shared between them).

To set this up from scratch (idempotent — safe to re-run):

```sh
~/development/knowledge-tools/scripts/install.sh
```

It generates the three units from the `scripts/knowledge-compile.*.in` templates — filling
in this repo's path for the worker script and the **vault** repo's path (`KNOWLEDGE_REPO`,
default `~/knowledge-vault`) for the inbox it watches and compiles — writes them into
`~/.config/systemd/user/`, reloads the daemon, enables and starts the timer + path watcher,
and enables linger so they run while you're logged out. To change a unit, edit its `.in`
template and re-run the script.

Point the install at a vault in a non-default location with `KNOWLEDGE_REPO`:

```sh
KNOWLEDGE_REPO=/path/to/knowledge-vault ~/development/knowledge-tools/scripts/install.sh
```
