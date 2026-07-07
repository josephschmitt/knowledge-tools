# knowledge-tools

Infrastructure for a personal "LLM wiki" — a knowledge base where raw captures land in
`inbox/` and a coding agent compiles them into durable, cross-linked notes in `library/`,
following Andrej Karpathy's [LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)
pattern: immutable raw sources, an LLM-owned wiki of markdown files, and a schema document
(`CLAUDE.md`) that defines the workflows. The agent is configurable — Claude Code by default,
or Codex / OpenCode / any custom command via `KNOWLEDGE_AGENT` — and the vault's procedures ship
as harness-neutral skills under `.agents/skills/`. This repo holds everything that operates *on*
the vault from the outside; the vault itself — the notes plus the `CLAUDE.md` librarian spec and
the `compile-inbox` skill the agent runs inside it — lives in a separate repo.

## Components

- **`service/`** — the vault service. One server exposing the vault over **two protocols**: a
  Streamable-HTTP **MCP** endpoint at `/mcp` (the claude.ai connector) and a **REST API** at
  `/api/v1` that mirrors the same operations for scripts and other tooling. Together they let you
  capture raw material into the vault's `inbox/` and query the compiled `library/`. Auth is
  **optional, off by default** and gates both surfaces: run it authless behind an authenticating
  proxy, or enable built-in OAuth token validation against any OIDC issuer. Reads/writes the vault
  via the `VAULT_ROOT` env var (bind-mounted into the container). Deployed separately — see
  [`service/README.md`](service/README.md).
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
- **`cli/`** — the host-side automation, a single Go CLI (`knowledge-tools`, alias `kt`) that
  replaced the old bash scripts. It runs the three vault-mutating jobs — `compile`
  (the `compile-inbox` skill), `synthesize`, and `resolve` (the two judgment-call jobs, carrying their
  calls over either GitHub issues or a git-free file queue; `KNOWLEDGE_REVIEW_CHANNEL`, see
  [below](#judgment-call-channel-github-or-files)) — driving a configurable agent harness
  (`KNOWLEDGE_AGENT`: claude by default, codex, opencode, or custom) and supervising them on a schedule via a
  self-managed `daemon` (one per vault), which `install`/`uninstall` register as a single OS
  autostart unit ([below](#vault-automation-host-setup)). `init` seeds a brand-new vault from
  `template/`. See [`cli/`](cli/).
- **`site/`** — the self-contained **`knowledge-site`** image: the
  [Quartz](https://quartz.jzhao.xyz) config plus a Dockerfile/entrypoint/server that build the
  library render **inside the container** from a bind-mounted vault (allowlist: only `index.md` +
  `library/`) and serve it on its **own URL**, rebuilding on a token-gated `POST /rebuild` the
  host's content jobs fire after a commit. Browser auth is handled by the proxy in front. Built and
  pushed to GHCR by CI — see [`site/README.md`](site/README.md).
- **`scripts/`** — only `validate_skills.py` remains (used by CI). The vault job/install scripts
  all moved into `cli/`.
- **`template/`** — the starting point of a vault's own librarian (`CLAUDE.md`, the
  `compile-inbox` / `synthesize` / `resolve` skills under `.agents/skills/` — the cross-client
  standard location, with `.claude/skills` symlinked to it for Claude — and the folder skeleton). A
  seed, not a source of truth — once `knowledge-tools init` copies it into a new vault, those files
  belong to the vault and are expected to drift as the corpus grows. See [Starting a new vault](#starting-a-new-vault).

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
KNOWLEDGE_REPO=/path/to/new-vault knowledge-tools init
# or pass the path directly:
knowledge-tools init /path/to/new-vault
```

This lays down the librarian (`CLAUDE.md`), the `compile-inbox`, `synthesize`, and
`resolve` skills (under `.agents/skills/`, with `.claude/skills` symlinked for Claude), and the
folder skeleton (`inbox/`, `library/`, `notebook/`, empty `index.md`/`log.md`).
It's a **one-shot scaffold, not a sync**: strictly copy-if-absent (existing files are never
touched, there's no `--force`), and it leaves git to you — `cd` into the new vault, `git init`,
and make the first commit. After seeding, the librarian belongs to the vault and is meant to
diverge from the template as your corpus grows; that drift is expected, not something to
reconcile. To reset a single file back to the seed, delete it and re-run.

**Migrating a vault seeded before the skills layout:** nothing to do. The jobs read each
procedure from `.agents/skills/<name>/SKILL.md` and **fall back** to the legacy
`.claude/commands/<name>.md` body when the skill isn't present, so an older vault keeps working
untouched (the job log prints a one-line nudge). To adopt the new layout — and get cross-harness
interactive invocation — run `knowledge-tools init` (copy-if-absent, so it won't clobber your tuned
files) and port each tuned command body into its `.agents/skills/<name>/SKILL.md`, then delete the
old `.claude/commands/`.

## Vault automation (host setup)

A single long-running **daemon** runs on the host per vault and supervises the three vault jobs on
an internal schedule — **one daemon per vault instance**, kept alive by one OS autostart unit (a
**systemd user service** on Linux, a **launchd LaunchAgent** on macOS) that `knowledge-tools
install` registers. On a single-vault host the instance is just `default` and you can ignore the
naming. The three jobs all edit `library/` and commit, so they share **one lockfile**
(`~/.local/state/knowledge-tools/vault-<vault>.lock`, keyed by the instance and overridable with
`KNOWLEDGE_VAULT_LOCK`) and never run concurrently — while different vaults have different locks and
*do* run concurrently. In every case the agent only edits files (and, in the GitHub review channel,
runs scoped `gh` calls) — the **wrapper** owns git: it commits any `library/` + `index.md` + `log.md`
changes and pushes if an `origin` remote exists. Which agent runs is set by `KNOWLEDGE_AGENT`
(`claude` by default; also `codex`, `opencode`, or a `custom` command), with per-job model and
reasoning-effort knobs — all set in the `.env` config file (copy `.env.example`) or the
environment. See `.env.example`.

The schedule, model/effort, and per-job gh tool grants can also live **in the vault itself**, in a
committed `<vault>/.knowledge-tools/config.yaml`, so the choice is git-versioned and travels with the
vault instead of the host:

```yaml
# <vault>/.knowledge-tools/config.yaml
defaults:            # agent-wide model/effort (per-job values below win)
  model: opus
  effort: xhigh
jobs:
  compile:
    schedule: "@hourly"
  synthesize:
    schedule: "CRON_TZ=America/Detroit 0 3 * * *"
    grants:          # gh subcommands this job may run unattended; replaces the built-in default
      - gh issue list
      - gh issue create
      - gh search issues
      - gh label list
      - gh label create
  resolve:
    schedule: "CRON_TZ=America/Detroit 0 4 * * *"
    effort: high
```

The Go struct that decodes this file **is** the allowlist: only `defaults.{model,effort}` and, for
the three known jobs, `{schedule,model,effort,grants}` are representable — any other key (a stray
`github_repo:`, `repo:`, …) simply decodes into nothing, so vault content can't touch
repo/git/site/auth wiring. Every knob is a default *below* the env: anything set in `.env`, the
environment (e.g. `KNOWLEDGE_SYNTHESIZE_GRANTS`), or an `install` flag overrides it, so a deployment
can always win without editing the vault. `grants` is a full replacement for that job's default list
(not a merge), applied only on the github review channel. The daemon reads this file once at startup,
so run `knowledge-tools daemon restart` after editing it — nothing needs to be re-installed. (It's
intentionally not created by `init` — model IDs, effort scales, schedules, and grants are
host/harness-specific, so seeded vaults stay neutral.)

The vault **need not be a git repo**: when the wrapper finds no work tree it skips the commit
and leaves history to whatever syncs the folder (Dropbox, Syncthing, …). Combined with the
file-based review channel below, that lets the whole system run on a plain synced folder with
no git and no GitHub.

The daemon schedules each job with a cron expression (timezone via a `CRON_TZ=` prefix). It also
runs a job once at startup if its scheduled tick elapsed while the daemon was down (catch-up), and
watches `inbox/.compile/request` so the MCP server's `compile_run` tool triggers an on-demand
compile. You can also run any job one-shot from the CLI for debugging:
`knowledge-tools {compile,synthesize,resolve}`.

**Compile** — turn fresh captures into notes. Default `@hourly` (`KNOWLEDGE_COMPILE_SCHEDULE`);
no-ops cheaply when the inbox is empty, so a tick only does real work when there are captures. The
MCP `compile_run` tool triggers it on demand (cooldown-throttled).

**Synthesize** (heavy, infrequent) and **Resolve** (light, frequent) — the judgment-call loop:

- **Synthesize** — a whole-corpus `synthesize` pass (default **weekly, ~4:30am local**,
  `KNOWLEDGE_SYNTHESIZE_SCHEDULE` = `CRON_TZ=America/Detroit 30 4 * * 0`). Reconciles
  drift/contradictions and finds new cross-note connections, **opening** judgment calls for
  anything only you can decide.
- **Resolve** — a `resolve` pass (default **daily, ~3:30am local**,
  `KNOWLEDGE_RESOLVE_SCHEDULE` = `CRON_TZ=America/Detroit 30 3 * * *`). Reads your answers to open
  calls, applies the decisions to `library/`, and **closes** them. Short-circuits cheaply when nothing
  is answered.

Both default to the middle of the night (pinned to `America/Detroit` so they track local night
through DST even on a UTC host) to land on off-peak Claude Max capacity, staggered an hour apart
and off the top of the hour so they don't collide with the hourly compile on the shared lock.

#### Judgment-call channel (GitHub or files)

Where a judgment call lives — and whether you need GitHub at all — is set by
`KNOWLEDGE_REVIEW_CHANNEL`. Unset, it **auto-detects**: `github` when `gh` is authed *and* an
`origin` remote exists, otherwise `files`.

- **`github`** — calls are GitHub issues. `synthesize` and `resolve` run `gh issue ...` from
  *inside* the Claude run, so the host needs the GitHub CLI installed, on PATH, and authenticated
  once with `gh auth login` (stored in `~/.config/gh`). The generated jobs put the relevant local
  profile dirs on PATH (`~/.nix-profile/bin` + `~/.local/bin` on Linux, the Homebrew prefixes on
  macOS) and rely on `HOME` for the auth; the run is
  granted only the exact `gh` subcommands configured for that job (the built-in `config.Default*Grants`,
  overridable per job via `KNOWLEDGE_<JOB>_GRANTS` or the vault's `jobs.<job>.grants`), passed to the
  agent via `--allowedTools` — never a blanket skip-permissions. The required labels
  (`vault:judgment-call`, `vault:needs-verification`, and `vault:answered`) are created on demand by
  `compile`/`synthesize` (both hold `gh label create`/`gh label list` grants), or you can pre-create
  them with `gh label create`. `synthesize` opens issues under the first
  two; you mark a settled one `vault:answered` and `resolve` then applies it (or asks a
  follow-up and clears the label).
- **`files`** — calls are markdown files in `inbox/.review/`, each with a `status` of
  `open → answered → applied`. `synthesize-files` opens them; you answer from chat through the
  MCP connector (`list_questions` / `get_question` / `answer_question`), which flips `status` to
  `answered`; `resolve-files` applies answered calls and marks them `applied`. **No `gh`, no
  GitHub, no git** — the run only edits files, so this is the channel for a vault synced as a
  plain folder.

Either way you can answer **from chat**: the MCP connector's `list_questions` / `get_question` /
`answer_question` tools work against both backends — the `inbox/.review/` queue by default, or the
GitHub issues directly when the server is configured with a token (`KNOWLEDGE_GITHUB_TOKEN` +
`KNOWLEDGE_GITHUB_REPO`; see [`service/README.md`](service/README.md#review-channel)). On the GitHub backend,
answering from chat comments and labels the issue `vault:answered` just as you would on
github.com, so `resolve` closes it — handy when you don't feel like opening GitHub. Point the
connector at the same channel the host uses.

To set this up from scratch (idempotent — safe to re-run), pass your vault path to `install` as a
positional arg (or set `KNOWLEDGE_REPO` — inline, or by copying `.env.example` to `.env`; the CLI
loads the repo-root `.env` automatically, and a real env var overrides it). With no path and no
`KNOWLEDGE_REPO`, `install` offers to use the current directory:

```sh
knowledge-tools install /path/to/vault
```

On **Linux** this writes a `knowledge-tools-daemon@.service` systemd user unit + this vault's
`~/.config/knowledge-tools/<vault>.env`, reloads the daemon, enables and starts
`knowledge-tools-daemon@<vault>`, and enables linger so it runs while you're logged out. Tail logs
with `journalctl --user -u knowledge-tools-daemon@<vault> -f`.

On **macOS** it writes one LaunchAgent (`com.knowledge-tools.daemon.<vault>.plist`, `KeepAlive`)
to `~/Library/LaunchAgents/` and (re)loads it with `launchctl bootstrap`. Logs go to
`~/Library/Logs/knowledge-tools/<vault>.log`. LaunchAgents run **only while you're logged in** (no
linger equivalent), so a night job needs the Mac on and logged in then. (The whole `OnCalendar →
launchd` grammar dance is gone — the daemon owns scheduling, so cron expressions work the same on
both OSes.)

Check state any time with `knowledge-tools job status` (prints the compile + schedule snapshots and
whether the daemon is running). After changing a schedule in the vault's `.knowledge-tools/config.yaml`,
`knowledge-tools daemon restart` picks it up; after changing one via `.env`/the environment, re-run
`knowledge-tools install` (it re-bakes the host override). On an existing single-vault host the first
install also removes the old bash-era per-job units it finds.

**Multiple vaults** (e.g. personal vs work) run on one host as separate instances — each its own
daemon, lock, schedule, and config. Just run `install` again per vault, passing its path and a
distinct `--instance` (the `<vault>` above; the first one defaults to `default`):

```sh
knowledge-tools install /path/to/work-vault --instance work
```

Each vault is a wholly independent deployment — its own service (one per vault, see
[`service/README.md`](service/README.md)), its own MCP connector, and its own daemon — rather than
one daemon multiplexing many vaults.

### Upgrading

Swapping the binary alone does **not** move a running daemon onto the new code — the OS keeps the
already-running process on the old binary until it's restarted. So an upgrade is two steps:

```sh
# 1. Install the new binary (Homebrew shown; or re-download the release archive)
brew upgrade --cask josephschmitt/tap/knowledge-tools

# 2. Roll the daemon onto it (per vault instance)
knowledge-tools daemon restart
```

`knowledge-tools daemon restart` re-applies everything `install` would (it rewrites the unit +
per-instance env so **new knobs and unit features take effect**) and then restarts the running
daemon — so it doubles as "re-run `install` and restart". Run it once per vault:

```sh
knowledge-tools daemon restart                          # the "default" vault
KNOWLEDGE_INSTANCE=work knowledge-tools daemon restart   # the "work" vault
```

`knowledge-tools job status` (and `knowledge-tools daemon status`) now records the running daemon's
version and flags when a newer binary is installed but the daemon hasn't been restarted onto it yet
— your cue to run the restart. The `daemon` command also has `start` and `stop` to pause/resume the
unit without uninstalling it. (`knowledge-tools daemon` with no subcommand still just runs the
daemon in the foreground — what the autostart unit invokes — so existing installs keep working
without a re-install.)

### Uninstalling

`knowledge-tools uninstall` is the reverse of `install` — same OS split (systemd / launchd), same
per-instance `KNOWLEDGE_INSTANCE` (default `default`), and idempotent (a no-op if that vault isn't
installed). Run it once per vault you want to remove:

```sh
knowledge-tools uninstall                   # remove the "default" vault
knowledge-tools uninstall --instance work   # remove the "work" vault
```

It stops and removes that instance's daemon unit and its per-vault config
(`~/.config/knowledge-tools/<vault>.env` on Linux) / log
(`~/Library/Logs/knowledge-tools/<vault>.log` on macOS). When the **last** vault is removed it also
cleans up the shared pieces — the systemd service template (Linux) and the empty logs dir (macOS) —
so nothing is left behind. It needs no `KNOWLEDGE_REPO`, and it deliberately leaves the **vault
itself** (`inbox/`, `library/`, `outputs/`), the optional `gh.env`, and linger untouched.

### Bot account (optional)

By default the issue jobs run `gh` as **you** (the `~/.config/gh` login), so any comment
`synthesize` or `resolve` posts is authored by your personal account — which reads as you
talking to yourself on your own issues. Purely cosmetic: it doesn't affect behavior, since
`resolve` gates on the `vault:answered` label, not on who wrote a comment. If the self-talk
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
