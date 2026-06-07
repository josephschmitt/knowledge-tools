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
- **`scripts/`** — the host-side automation. `vault-compile.sh` runs an ephemeral Claude
  Code pass (`/compile-inbox`) over the vault; `vault-job.sh` runs the two GitHub-issue jobs
  (`/synthesize` and `/resolve`); `vault-lib.sh` holds the config, shared cross-job lock, and
  git side effects all three share; `install.sh` generates the systemd *user* units from the
  `knowledge-*.in` templates; `init-vault.sh` seeds a brand-new vault from `template/`;
  `validate_skills.py` is used by CI.
- **`template/`** — the starting point of a vault's own librarian (`CLAUDE.md`, the
  `/compile-inbox`, `/synthesize`, `/resolve` commands, and the folder skeleton). A seed, not
  a source of truth — once `init-vault.sh` copies it into a new vault, those files belong to
  the vault and are expected to drift as the corpus grows. See [Starting a new vault](#starting-a-new-vault).

## Installing the skill

**Claude Code** — this repo is a plugin marketplace (`tools`) holding one plugin
(`knowledge`) that bundles the `skills/` directory:

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install knowledge@tools
```

The skill is then invocable as `/knowledge:knowledge-vault`, and `/plugin update` tracks
`main`. The plugin bundles the skill's MCP connector: enabling it prompts for your
self-hosted MCP URL (including the `/mcp` path, e.g. `https://knowledge.example.com/mcp`)
and wires it up as a remote HTTP server. OAuth is negotiated automatically on first
connect — the server advertises its Cloudflare Access authorization server and Claude Code
walks the flow (run `/mcp` if you need to (re)authenticate). The vault must already be
deployed and reachable at that URL — see [`mcp/README.md`](mcp/README.md).

**claude.ai** — download the `knowledge-vault.zip` asset from the latest
[release](https://github.com/josephschmitt/knowledge-tools/releases) and upload it as a
skill. CI builds these zips on every skill change merged to `main`.

## Starting a new vault

If you don't already have a vault repo, seed one from the bundled `template/`:

```sh
KNOWLEDGE_REPO=/path/to/new-vault scripts/init-vault.sh
# or pass the path directly:
scripts/init-vault.sh /path/to/new-vault
```

This lays down the librarian (`CLAUDE.md`), the `/compile-inbox`, `/synthesize`, and
`/resolve` commands, and the folder skeleton (`inbox/`, `wiki/`, empty `index.md`/`log.md`).
It's a **one-shot scaffold, not a sync**: strictly copy-if-absent (existing files are never
touched, there's no `--force`), and it leaves git to you — `cd` into the new vault, `git init`,
and make the first commit. After seeding, the librarian belongs to the vault and is meant to
diverge from the template as your corpus grows; that drift is expected, not something to
reconcile. To reset a single file back to the seed, delete it and re-run.

## Vault automation (host setup)

Three vault jobs run on the host as systemd *user* units. They all edit `wiki/` and commit,
so they share **one lockfile** (`~/.local/state/knowledge-tools/vault.lock`, overridable with
`KNOWLEDGE_VAULT_LOCK`) and never run concurrently. In every case Claude only edits files (and,
for the issue jobs, runs scoped `gh` calls) — the **wrapper** owns git: it commits any
`wiki/` + `index.md` + `log.md` changes and pushes if an `origin` remote exists.

**Compile** — turn fresh captures into notes:

- `knowledge-compile.service` — one ephemeral inbox→wiki compile (the worker).
- `knowledge-compile.timer` — runs it on a schedule (default hourly;
  `KNOWLEDGE_COMPILE_ONCALENDAR`). No-ops cheaply when the inbox is empty, so a tick only does
  real work when there are captures.
- `knowledge-compile.path` — runs it on demand when the MCP server's `compile_run` tool drops
  `inbox/.compile/request` into the vault. Both triggers start the same service.

**Synthesize** (heavy, infrequent) and **Resolve** (light, frequent) — the GitHub-issue loop:

- `knowledge-synthesize.{service,timer}` — a whole-corpus `/synthesize` pass (default **weekly,
  ~4:30am local**, `KNOWLEDGE_SYNTHESIZE_ONCALENDAR`). Reconciles drift/contradictions and finds
  new cross-note connections, **opening** judgment-call issues for anything only you can decide.
- `knowledge-resolve.{service,timer}` — a `/resolve` pass (default **daily, ~3:30am local**,
  `KNOWLEDGE_RESOLVE_ONCALENDAR`). Reads your answers on open issues, applies the decisions to
  `wiki/`, and **closes** them. Short-circuits cheaply when no issues are open.

Both default to the middle of the night (pinned to `America/Detroit` so they track local night
through DST even on a UTC host) to land on off-peak Claude Max capacity, staggered an hour apart
and off the top of the hour so they don't collide with the hourly compile on the shared lock.

> **`gh` auth required for synthesize/resolve** (not compile). These two run `gh issue ...`
> from *inside* the Claude run — that's how issues get filed and closed — so the host needs the
> GitHub CLI installed, on PATH, and authenticated once with `gh auth login` (stored in
> `~/.config/gh`). The generated service units put `~/.nix-profile/bin` and `~/.local/bin` on
> PATH and rely on `HOME` for the auth; the run is granted only the exact `gh issue`
> subcommands each command's frontmatter declares (via `--allowedTools`), never a blanket
> skip-permissions. The required labels (`vault:judgment-call`, `vault:needs-verification`,
> and `vault:answered`) must exist on the repo — create them once with `gh label create`.
> `/synthesize` opens issues under the first two; you mark a settled one `vault:answered` and
> `/resolve` then applies it (or asks a follow-up and clears the label), so `/resolve`'s grant
> includes `gh issue edit` to manage that label.

To set this up from scratch (idempotent — safe to re-run), point `KNOWLEDGE_REPO` at your
vault repo — either inline as below, or by copying `.env.example` to `.env` and setting it
there (the scripts load the repo-root `.env` automatically; a real env var overrides it):

```sh
KNOWLEDGE_REPO=/path/to/vault ~/development/knowledge-tools/scripts/install.sh
```

It generates the units from the `scripts/knowledge-*.in` templates — filling in this repo's
path for the worker scripts and the **vault** repo's path (from the required `KNOWLEDGE_REPO`)
— writes them into `~/.config/systemd/user/`, reloads the daemon, enables and starts the three
timers + the path watcher, and enables linger so they run while you're logged out. Run the
issue jobs on demand with `systemctl --user start knowledge-{synthesize,resolve}.service`. To
change a unit, edit its `.in` template and re-run the script.

### Bot account (optional)

By default the issue jobs run `gh` as **you** (the `~/.config/gh` login), so any comment
`/synthesize` or `/resolve` posts is authored by your personal account — which reads as you
talking to yourself on your own issues. Purely cosmetic: it doesn't affect behavior, since
`/resolve` gates on the `vault:answered` label, not on who wrote a comment. If the self-talk
bugs you, give the jobs a separate **machine account** (GitHub permits one per person for
automation, and it's free — including on private repos):

1. Create a second GitHub account with a distinct email — a plus-alias like
   `you+vaultbot@example.com` works, so you don't need a new inbox.
2. Invite it to the vault repo as a collaborator with **write** access; accept from the bot.
3. On the bot account, create a **fine-grained PAT** scoped to just the vault repo with
   **Issues: read/write**.
4. Drop it where the units already look for it (this path is wired into the service templates
   as an *optional* `EnvironmentFile`, so it's inert until the file exists):

   ```sh
   install -d -m 700 ~/.config/knowledge-tools
   printf 'GH_TOKEN=%s\n' '<the-bot-PAT>' > ~/.config/knowledge-tools/gh.env
   chmod 600 ~/.config/knowledge-tools/gh.env
   ```

`gh` honors `GH_TOKEN` over the keyring, so the next run authenticates as the bot. **Until
that file exists, nothing changes** — `gh` falls back to your `~/.config/gh` login and the jobs
keep working exactly as before. To revert, delete the file. The token must never be committed;
keeping it in `~/.config` (not the repo) is the point.
