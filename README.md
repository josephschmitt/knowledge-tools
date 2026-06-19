# knowledge-tools

Infrastructure for a personal "LLM wiki" — a knowledge base where raw captures land in
`inbox/` and Claude Code compiles them into durable, cross-linked notes in `wiki/`,
following Andrej Karpathy's [LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)
pattern: immutable raw sources, an LLM-owned wiki of markdown files, and a schema document
(`CLAUDE.md`) that defines the workflows. This repo holds everything that operates *on* the
vault from the outside; the vault itself — the notes plus the `CLAUDE.md` librarian spec and
`/compile-inbox` command Claude runs inside it — lives in a separate repo.

## Components

- **`service/`** — the vault service. One server exposing the vault over **two protocols** plus an
  optional **static website**: a Streamable-HTTP **MCP** endpoint at `/mcp` (the claude.ai
  connector), a **REST API** at `/api/v1` that mirrors the same operations for scripts and other
  tooling, and (opt-in) a browsable **[Quartz](https://quartz.jzhao.xyz) rendering of the wiki** at
  `/`. The two protocols let you capture raw material into the vault's `inbox/` and query the
  compiled `wiki/`; the static site is a pre-built artifact you build on the host with
  `scripts/vault-site.sh` and bind-mount in (`KNOWLEDGE_ENABLE_SITE` + `KNOWLEDGE_SITE_ROOT`). Auth
  is **optional, off by default** and gates all surfaces: run it authless behind an authenticating
  proxy, or enable built-in OAuth token validation against any OIDC issuer. Reads/writes the vault
  via the `VAULT_ROOT` env var
  (bind-mounted into the container). Deployed separately — see [`service/README.md`](service/README.md).
- **`plugins/`** — the Claude Code plugins, one per `plugins/<plugin>/`, each bundling a
  single skill plus (where needed) its MCP connector config. Two of them:
  - **`plugins/vault/`** (skill `knowledge-vault`) — the conversational front-door skill:
    capture and query, delegating heavy compilation to the host. Carries the MCP connector
    config. Ships two ways — as a Claude Code plugin (see
    [Installing the skill](#installing-the-skill)) and as a per-skill release zip for claude.ai.
  - **`plugins/auto-capture/`** (skill `auto-capture`) — an optional, opt-in always-on skill
    that proactively captures capture-worthy material to the inbox *without* being asked, so
    nothing is lost just because you forgot to say "save this". Reuses the `vault` plugin's
    `append_to_inbox` connector; it never queries or compiles. Ships as a separate Claude Code
    plugin and its own release zip.
- **`scripts/`** — the host-side automation. `vault-compile.sh` runs an ephemeral Claude
  Code pass (`/compile-inbox`) over the vault; `vault-job.sh` runs the two judgment-call jobs
  (`/synthesize` and `/resolve`), carrying their judgment calls over either GitHub issues or a
  git-free file queue (`KNOWLEDGE_REVIEW_CHANNEL`, see [below](#judgment-call-channel-github-or-files));
  `vault-lib.sh` holds the config, per-vault cross-job lock, and
  git side effects all three share; `vault-site.sh` builds the optional
  [Quartz](https://quartz.jzhao.xyz) static site the service serves at `/` (renders `index.md` +
  `wiki/` only, publishes outside the vault — see [`service/README.md`](service/README.md#static-website-));
  `install.sh` generates the host scheduler jobs from the `knowledge-*` templates (systemd *user*
  units on Linux, launchd agents on macOS) — one instance per vault, so a host can run several
  ([below](#vault-automation-host-setup)); `init-vault.sh` seeds a brand-new vault from `template/`;
  `validate_skills.py` is used by CI.
- **`template/`** — the starting point of a vault's own librarian (`CLAUDE.md`, the
  `/compile-inbox`, `/synthesize`, `/resolve` commands, and the folder skeleton). A seed, not
  a source of truth — once `init-vault.sh` copies it into a new vault, those files belong to
  the vault and are expected to drift as the corpus grows. See [Starting a new vault](#starting-a-new-vault).

## Installing the skill

**Claude Code** — this repo is a plugin marketplace (`knowledge-tools`) with one plugin per
skill, so each installs independently:

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install vault@knowledge-tools          # core: capture-on-request + query
/plugin install auto-capture@knowledge-tools   # optional: autonomous capture (needs vault)
```

The skills are then invocable as `/vault:knowledge-vault` and `/auto-capture:auto-capture`,
and `/plugin update` tracks `main`. `auto-capture` is opt-in and reuses the `vault` plugin's
connector, so install `vault` too (or on its own it has nothing to capture through). The
`vault` plugin bundles the skill's MCP connector: enabling it prompts for your
self-hosted MCP URL (including the `/mcp` path, e.g. `https://knowledge.example.com/mcp`)
and wires it up as a remote HTTP server. OAuth is negotiated automatically on first
connect — the authenticating proxy/IdP in front of the server advertises the authorization
server and Claude Code walks the flow (run `/mcp` if you need to (re)authenticate). The vault
must already be deployed and reachable at that URL — see
[`service/README.md`](service/README.md).

### If your IdP doesn't support Dynamic Client Registration

The auto-negotiation above relies on your authorization server supporting **OAuth Dynamic
Client Registration** (DCR) — many hosted IdPs do, so Claude Code registers a client on the
fly and there's nothing to configure. Some self-hosted IdPs (e.g. **Authelia**) don't support
DCR, so Claude Code can't auto-register and fails with
*"Incompatible auth server: does not support dynamic client registration."*

For that case, pre-register a **public client with PKCE** in your IdP (no client secret — it's a
CLI) with a fixed loopback redirect. Then point Claude Code at that client through a
**`.mcp.json`** entry — *not* the plugin's config. The plugin can't carry the client ID itself:
its connector is wired from `userConfig`, and Claude Code does **not** interpolate
`${user_config.*}` into the `oauth` block (only into `url`), so a client ID supplied through the
plugin reaches the IdP as the literal string `${user_config.oauth_client_id}` and is rejected.
A `.mcp.json` entry takes literal values and **overrides** the plugin's same-named server, so
this is the supported path.

1. **Register the client** in your IdP with the loopback redirect the entry below uses. IdPs
   match redirect URIs exactly and native OAuth clients disagree on the loopback host, so
   register **both** `http://127.0.0.1:47832/callback` and `http://localhost:47832/callback`.

2. **Add the server to a `.mcp.json`** — a project one, or `~/.mcp.json` to cover every project
   (Claude reads `.mcp.json` from the working directory up to the filesystem root):

   ```json
   {
     "mcpServers": {
       "knowledge-vault": {
         "type": "http",
         "url": "https://knowledge.example.com/mcp",
         "oauth": { "clientId": "<your-public-client-id>", "callbackPort": 47832 }
       }
     }
   }
   ```

   The `callbackPort` must match the port in the redirect URIs you registered.

3. **Approve + authenticate.** Reload; approve the `knowledge-vault` server when Claude prompts
   (*"New MCP server found in .mcp.json"*), or set `enableAllProjectMcpServers` /
   `enabledMcpjsonServers` in `settings.json`. Then `/mcp` → **Authenticate**.

DCR deployments need none of this — leave the plugin's bare server alone and Claude Code
auto-registers on first connect.

**claude.ai** — download the `knowledge-vault.zip` asset (and, optionally,
`auto-capture.zip`) from the latest
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

Three vault jobs run on the host as scheduled user-level jobs — **systemd user units on Linux**,
**launchd LaunchAgents on macOS** (`install.sh` picks the right one; see the macOS note below) —
as one **template instance per vault** (`knowledge-compile@<vault>.service`, …). On a single-vault
host the instance is just `default` and you can ignore the naming. They all edit `wiki/` and commit, so the three jobs **for a given
vault** share **one lockfile** (`~/.local/state/knowledge-tools/vault-<vault>.lock`, keyed by the
instance and overridable with `KNOWLEDGE_VAULT_LOCK`) and never run concurrently — while different
vaults have different locks and *do* run concurrently. In every case Claude only edits files (and,
in the GitHub review channel, runs scoped `gh` calls) — the **wrapper** owns git: it commits any
`wiki/` + `index.md` + `log.md` changes and pushes if an `origin` remote exists.

The vault **need not be a git repo**: when the wrapper finds no work tree it skips the commit
and leaves history to whatever syncs the folder (Dropbox, Syncthing, …). Combined with the
file-based review channel below, that lets the whole system run on a plain synced folder with
no git and no GitHub.

**Compile** — turn fresh captures into notes:

- `knowledge-compile@<vault>.service` — one ephemeral inbox→wiki compile (the worker).
- `knowledge-compile@<vault>.timer` — runs it on a schedule (default hourly;
  `KNOWLEDGE_COMPILE_ONCALENDAR`). No-ops cheaply when the inbox is empty, so a tick only does
  real work when there are captures.
- `knowledge-compile@<vault>.path` — runs it on demand when the MCP server's `compile_run` tool
  drops `inbox/.compile/request` into the vault. Both triggers start the same service.

**Synthesize** (heavy, infrequent) and **Resolve** (light, frequent) — the judgment-call loop:

- `knowledge-synthesize@<vault>.{service,timer}` — a whole-corpus `/synthesize` pass (default
  **weekly, ~4:30am local**, `KNOWLEDGE_SYNTHESIZE_ONCALENDAR`). Reconciles drift/contradictions
  and finds new cross-note connections, **opening** judgment calls for anything only you can decide.
- `knowledge-resolve@<vault>.{service,timer}` — a `/resolve` pass (default **daily, ~3:30am
  local**, `KNOWLEDGE_RESOLVE_ONCALENDAR`). Reads your answers to open calls, applies the decisions
  to `wiki/`, and **closes** them. Short-circuits cheaply when nothing is answered.

Both default to the middle of the night (pinned to `America/Detroit` so they track local night
through DST even on a UTC host) to land on off-peak Claude Max capacity, staggered an hour apart
and off the top of the hour so they don't collide with the hourly compile on the shared lock.

#### Judgment-call channel (GitHub or files)

Where a judgment call lives — and whether you need GitHub at all — is set by
`KNOWLEDGE_REVIEW_CHANNEL`. Unset, it **auto-detects**: `github` when `gh` is authed *and* an
`origin` remote exists, otherwise `files`.

- **`github`** — calls are GitHub issues. `/synthesize` and `/resolve` run `gh issue ...` from
  *inside* the Claude run, so the host needs the GitHub CLI installed, on PATH, and authenticated
  once with `gh auth login` (stored in `~/.config/gh`). The generated jobs put the relevant local
  profile dirs on PATH (`~/.nix-profile/bin` + `~/.local/bin` on Linux, the Homebrew prefixes on
  macOS) and rely on `HOME` for the auth; the run is
  granted only the exact `gh issue` subcommands each command's frontmatter declares (via
  `--allowedTools`), never a blanket skip-permissions. The required labels
  (`vault:judgment-call`, `vault:needs-verification`, and `vault:answered`) must exist on the
  repo — create them once with `gh label create`. `/synthesize` opens issues under the first
  two; you mark a settled one `vault:answered` and `/resolve` then applies it (or asks a
  follow-up and clears the label).
- **`files`** — calls are markdown files in `inbox/.review/`, each with a `status` of
  `open → answered → applied`. `/synthesize-files` opens them; you answer from chat through the
  MCP connector (`list_questions` / `get_question` / `answer_question`), which flips `status` to
  `answered`; `/resolve-files` applies answered calls and marks them `applied`. **No `gh`, no
  GitHub, no git** — the run only edits files, so this is the channel for a vault synced as a
  plain folder.

Either way you can answer **from chat**: the MCP connector's `list_questions` / `get_question` /
`answer_question` tools work against both backends — the `inbox/.review/` queue by default, or the
GitHub issues directly when the server is configured with a token (`KNOWLEDGE_GITHUB_TOKEN` +
`KNOWLEDGE_GITHUB_REPO`; see [`service/README.md`](service/README.md#review-channel)). On the GitHub backend,
answering from chat comments and labels the issue `vault:answered` just as you would on
github.com, so `/resolve` closes it — handy when you don't feel like opening GitHub. Point the
connector at the same channel the host uses.

To set this up from scratch (idempotent — safe to re-run), point `KNOWLEDGE_REPO` at your
vault repo — either inline as below, or by copying `.env.example` to `.env` and setting it
there (the scripts load the repo-root `.env` automatically; a real env var overrides it):

```sh
KNOWLEDGE_REPO=/path/to/vault ~/development/knowledge-tools/scripts/install.sh
```

It generates the units from the `scripts/knowledge-*@.{service,timer,path}.in` templates —
filling in this repo's path for the worker scripts and writing this vault's `KNOWLEDGE_REPO` into
`~/.config/knowledge-tools/<vault>.env` (which the service units load) — installs them into
`~/.config/systemd/user/`, reloads the daemon, enables and starts the three timers + the path
watcher, and enables linger so they run while you're logged out. Run the issue jobs on demand with
`systemctl --user start knowledge-{synthesize,resolve}@<vault>.service`. To change a unit, edit its
`.in` template and re-run the script.

**On macOS** the same command instead generates three launchd LaunchAgents per vault
(`com.knowledge-tools.{compile,synthesize,resolve}.<vault>.plist`) from the
`scripts/knowledge-{compile,synthesize,resolve}.plist.in` templates, writes them to
`~/Library/LaunchAgents/`, and (re)loads each with `launchctl bootstrap`. The compile agent folds
both triggers into one job — its schedule plus a `WatchPaths` watcher on `inbox/.compile/request`.
Run the issue jobs on demand with
`launchctl kickstart -k gui/$(id -u)/com.knowledge-tools.{synthesize,resolve}.<vault>`, and tail
logs at `~/Library/Logs/knowledge-tools/<vault>-<job>.log` (there's no `journalctl`). Two caveats:
cadences are scheduled in the Mac's **local time** (a trailing timezone like `America/Detroit` is
dropped) and accept only a subset of the OnCalendar grammar (hourly/daily/weekly,
`[Dow ]*-*-* HH:MM:SS`, and every-N-min `*-*-* *:MM/STEP:SS`); and LaunchAgents run **only while
you're logged in** (no linger equivalent), so a night job needs the Mac on and logged in then.

**Multiple vaults** (e.g. personal vs work) run on one host as separate instances — each its own
deployment of these units, lock, schedule, and config. Just run `install.sh` again per vault with a
distinct `KNOWLEDGE_INSTANCE` (the `<vault>` above; the first one defaults to `default`):

```sh
KNOWLEDGE_INSTANCE=work KNOWLEDGE_REPO=/path/to/work-vault ~/development/knowledge-tools/scripts/install.sh
```

Each vault is a wholly independent deployment — its own service (one per vault, see
[`service/README.md`](service/README.md)), its own MCP connector, and its own host jobs — rather than
one service multiplexing many vaults. Re-running `install.sh` once on an existing single-vault host
migrates it to the `default` instance (it removes the old non-instanced units).

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

> **macOS:** the `gh.env` file is **Linux-only** — it relies on systemd's optional
> `EnvironmentFile`, which launchd has no equivalent for (and baking a PAT into a LaunchAgent plist
> would persist the secret in `~/Library/LaunchAgents`, a worse posture). For a bot identity on
> macOS, log `gh` in as the bot directly (`gh auth login` as that account), or export `GH_TOKEN`
> in the environment the LaunchAgents inherit.
