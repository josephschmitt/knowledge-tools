# knowledge-tools

Infrastructure for a personal "LLM wiki" — a knowledge base where raw captures land in
`inbox/` and Claude Code compiles them into durable, cross-linked notes in `wiki/`,
following Andrej Karpathy's [LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)
pattern: immutable raw sources, an LLM-owned wiki of markdown files, and a schema document
(`CLAUDE.md`) that defines the workflows. This repo holds everything that operates *on* the
vault from the outside; the vault itself — the notes plus the `CLAUDE.md` librarian spec and
`/compile-inbox` command Claude runs inside it — lives in a separate repo.

## Components

- **`mcp/`** — the claude.ai connector. A Streamable-HTTP MCP server (OAuth 2.1 resource
  server, gated by Cloudflare Access OIDC) that lets claude.ai capture raw material into the
  vault's `inbox/` and query the compiled `wiki/`. Reads/writes the vault via the `VAULT_ROOT`
  env var (bind-mounted into the container). Deployed separately — see [`mcp/README.md`](mcp/README.md).
- **`skills/knowledge-vault/`** — the conversational front-door skill: capture and query,
  delegating heavy compilation to the host. Ships two ways — as a Claude Code plugin (see
  [Installing the skill](#installing-the-skill)) and as per-skill release zips for claude.ai.
- **`scripts/`** — the host-side compile automation. `nightly-compile.sh` runs an ephemeral
  Claude Code pass (`/compile-inbox`) over the vault; `install.sh` generates the systemd
  *user* units from the `knowledge-compile.*.in` templates; `validate_skills.py` is used by CI.

## Installing the skill

**Claude Code** — this repo is a plugin marketplace (`tools`) holding one plugin
(`knowledge`) that bundles the `skills/` directory:

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install knowledge@tools
```

The skill is then invocable as `/knowledge:knowledge-vault`, and `/plugin update` tracks
`main`. The skill drives the vault through its MCP connector, which must be configured
separately for it to function.

**claude.ai** — download the `knowledge-vault.zip` asset from the latest
[release](https://github.com/josephschmitt/knowledge-tools/releases) and upload it as a
skill. CI builds these zips on every skill change merged to `main`.

## Compile automation (host setup)

Compiling the inbox into the wiki runs on the host as systemd *user* units:

- `knowledge-compile.service` — one ephemeral inbox→wiki compile (the worker).
- `knowledge-compile.timer` — runs it nightly at 03:00.
- `knowledge-compile.path` — runs it on demand when the MCP server's `compile_run` tool
  drops `inbox/.compile/request` into the vault. Both triggers start the same service, so
  systemd runs only one compile at a time (the lock shared between them).

To set this up from scratch (idempotent — safe to re-run), point `KNOWLEDGE_REPO` at your
vault repo — either inline as below, or by copying `.env.example` to `.env` and setting it
there (the scripts load the repo-root `.env` automatically; a real env var overrides it):

```sh
KNOWLEDGE_REPO=/path/to/vault ~/development/knowledge-tools/scripts/install.sh
```

It generates the three units from the `scripts/knowledge-compile.*.in` templates — filling
in this repo's path for the worker script and the **vault** repo's path (from the required
`KNOWLEDGE_REPO`) for the inbox it watches and compiles — writes them into
`~/.config/systemd/user/`, reloads the daemon, enables and starts the timer + path watcher,
and enables linger so they run while you're logged out. To change a unit, edit its `.in`
template and re-run the script.
