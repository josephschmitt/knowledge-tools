# Knowledge Vault Service

One server that exposes this knowledge vault over **two protocols** plus an optional **static
website**, all backed by the same in-process vault core:

- **MCP** at `/mcp` — a remote [MCP](https://modelcontextprotocol.io) endpoint over the
  **Streamable HTTP** transport, used by **claude.ai as a custom connector** (and the Claude
  Code plugin). The MCP *protocol* server name is `knowledge-vault`.
- **REST** at `/api/v1` — a plain JSON HTTP API mirroring the MCP tools 1:1, for scripts,
  automation, and any other tooling that doesn't speak MCP. See [REST API](#rest-api).
- **Static site** at `/` — an optional, pre-built [Quartz](https://quartz.jzhao.xyz) rendering of
  the wiki you can browse in a web browser. **Off by default**; see [Static website](#static-website-).

The two protocols are on by default; the static site is opt-in. Each surface toggles independently
with `KNOWLEDGE_ENABLE_MCP` / `KNOWLEDGE_ENABLE_REST` / `KNOWLEDGE_ENABLE_SITE` (a disabled
surface's paths 404; the server refuses to start if all three are off). See
[Choosing which surfaces to serve](#choosing-which-surfaces-to-serve).

Authentication is **optional and off by default** and gates **both** surfaces: out of the box
the server does no auth and trusts its network (run it behind an authenticating proxy), or you
can switch on built-in OAuth token validation pointed at any OIDC issuer. See
[Authentication](#authentication).

> **Renamed from `knowledge-mcp`.** This service used to be MCP-only and shipped as the image
> `ghcr.io/josephschmitt/knowledge-mcp`. Now that it serves REST too, the directory, package,
> and **image are `knowledge-service`**. This is a breaking change for existing deployments:
> point your compose at `ghcr.io/josephschmitt/knowledge-service` — the old `knowledge-mcp` tag
> no longer updates.

## Tools

| Tool | Purpose |
|---|---|
| `search_wiki(query)` | Full-text search across compiled wiki notes |
| `get_note(path)` | Return one note's markdown |
| `list_index()` | Return `index.md` (the navigation map) |
| `list_notes()` | List every wiki note |
| `append_to_inbox(text, title?)` | Capture a raw note into `inbox/` for the scheduled compile |
| `compile_run()` | Trigger an on-demand compile (async, rate-limited to one/hour) |
| `vault_status()` | Pollable JSON: last successful compile time, pending inbox count, manual-compile cooldown, running flag |
| `list_questions(status?)` | List judgment calls the vault is waiting on (file review channel) |
| `get_question(id)` | Return one judgment call's full markdown |
| `answer_question(id, answer)` | Record a decision on a judgment call (writes `inbox/.review/`, marks it answered) |

`append_to_inbox` is the capture path: drop a thought from claude.ai, and the scheduled
compile (`scripts/vault-compile.sh`) folds it into the wiki.

The `*_question` tools are the inbound half of the **judgment-call channel** — the calls the
weekly maintenance pass can't decide on its own, answered from chat. They work against either
backend (see [Review channel](#review-channel) below):

- **files** (default) — `/synthesize-files` files calls as markdown in `inbox/.review/`, you
  answer them here, `/resolve-files` applies them. Like `append_to_inbox` and the compile
  sentinel, `answer_question` writes only under `inbox/`, staying inside the least-privilege
  write mount — no external creds or egress.
- **github** — the calls are the vault's GitHub issues; `answer_question` comments the answer and
  adds the `vault:answered` label, exactly what answering on github.com does, so the host's
  `/resolve` applies and closes it.

See the root README's
[judgment-call channel](../README.md#judgment-call-channel-github-or-files) for the host side.

### Review channel

`list_questions` / `get_question` / `answer_question` default to the **files** backend
(`inbox/.review/`), which needs nothing beyond the vault mount. To back them with **GitHub
issues** instead, set `KNOWLEDGE_GITHUB_TOKEN` (a PAT with `issues:read`+`write` on the vault repo) and
`KNOWLEDGE_GITHUB_REPO` (`owner/repo`); the server then reaches the GitHub REST API over the network.
The channel auto-detects (`github` when both are set, else `files`) and `KNOWLEDGE_REVIEW_CHANNEL`
forces it. The host's synthesize/resolve jobs read a same-named `KNOWLEDGE_REVIEW_CHANNEL`, so
**set it to the same value in both places** and both halves of the loop share one surface. This is
the only feature that gives the server outbound network access and a credential, so it stays off
until you configure it.

### Manual compile (`compile_run`)

The server can't compile in-process — the vault is read-only here except `inbox/`, and
synthesis needs the `claude` CLI + git on the host. So `compile_run` *triggers* the host
compile and reports status; it doesn't wait for the result. It writes a sentinel to
`inbox/.compile/request` (the one writable path), which a systemd `.path` unit
(`scripts/knowledge-compile@.path.in`, one instance per vault) watches to start the same
`knowledge-compile@<vault>.service` the scheduled timer uses — so systemd runs one compile at a
time per vault (the per-vault lock). The
host writes `inbox/.compile/status.json`, which the server reads to return `triggered` /
`throttled` (refused within the one-hour cooldown) / `busy` / `empty`. The scheduled
run is never throttled and doesn't consume the manual cooldown.

Because `compile_run` returns before the compile finishes, `vault_status` is the completion
signal: the host records `last_compiled_at` at the *end* of every successful compile (both
scheduled and on-demand), so a `last_compiled_at` newer than your trigger time means the run
finished. It also reports `pending_inbox_count` and `manual_compile_available_at` (when the
cooldown next clears) — poll it after a `compile_run` to know when the wiki is caught up.

## Choosing which surfaces to serve

The two protocols run by default; the static site is opt-in. Toggle each independently:

```sh
KNOWLEDGE_ENABLE_MCP=false     # turn off MCP  (e.g. REST/site only)
KNOWLEDGE_ENABLE_REST=false    # turn off REST (e.g. MCP/site only)
KNOWLEDGE_ENABLE_SITE=true     # turn ON the static site (off by default; see Static website)
```

A disabled surface isn't mounted, so its paths return `404` (and for MCP, the RFC 9728 discovery
metadata isn't advertised either). `/healthz` is always served. Setting **all three** off is a
misconfiguration — the server logs `FATAL … nothing to serve` and exits non-zero.

## Static website (`/`)

Optionally serve a browsable **[Quartz](https://quartz.jzhao.xyz) rendering of the wiki** at `/`,
alongside `/mcp` and `/api/v1` — a fast static site with full-text search, backlinks, a graph view,
and Obsidian-style `[[wikilink]]` navigation. **Off by default**; turn it on with:

```sh
KNOWLEDGE_ENABLE_SITE=true
KNOWLEDGE_SITE_ROOT=/site      # where the pre-built site is mounted (default /site)
```

**Quartz is a build-time generator, not a renderer** — the server only *serves* a pre-built
directory; it never runs Quartz and carries none of its dependencies. So there are two pieces:

1. **Build the artifact** on the host with [`scripts/vault-site.sh`](../scripts/vault-site.sh). It
   renders only `index.md` + `wiki/` (a strict allowlist — never `inbox/`, `outputs/`, logs, or
   task files) and publishes the static output **outside** the vault, swapped in atomically so the
   server never sees a half-built tree. See [Building the site](#building-the-site).
2. **Serve it** — bind-mount that output directory into the container at `KNOWLEDGE_SITE_ROOT` and
   set `KNOWLEDGE_ENABLE_SITE=true`. A single `express.static` serves Quartz's clean URLs
   (`/wiki/foo`); an unmatched path returns Quartz's `404.html`. The directory needn't exist at
   startup (the host populates it asynchronously) — the server logs a warning and serves it once it
   appears.

```sh
# Add to your `docker run` / compose alongside the existing VAULT_ROOT mount:
#   -v ~/.local/state/knowledge-tools/site/default:/site:ro   # the built site (read-only)
#   -e KNOWLEDGE_ENABLE_SITE=true
```

> **Browser access needs a proxy when built-in auth is on.** The `/` surface is gated by the
> **same `requireToken`** as the other surfaces. With built-in token validation on
> ([Authentication](#authentication)), a plain browser sends no `Bearer` token and gets `401` — the
> server is a pure resource server with no login page. So browse it behind the **same
> authenticating proxy** that fronts the rest of the service, which logs the browser in and
> injects the header. With auth off (the default), `requireToken` is
> a no-op and the proxy in front is the gate, exactly like `/mcp` and `/api/v1`.

### Building the site

`scripts/vault-site.sh` runs on the **host** (where the vault and Node live, not in the container).
It maintains a pinned Quartz checkout, overlays the config in [`site/`](../site), stages
`index.md` + `wiki/`, runs `quartz build`, and atomically swaps the result into the output
directory. It's read-only w.r.t. the vault (no git, no commits), so it's safe to run any time — by
hand, from cron, or from a systemd timer:

```sh
# Needs Node >= 20 on the host (for Quartz). Build the default vault's site once:
KNOWLEDGE_REPO=/path/to/vault scripts/vault-site.sh
# Output: ~/.local/state/knowledge-tools/site/<instance>/ — bind-mount THAT dir into the container.
```

Host knobs (set in the repo-root `.env` or the environment):

| Var | Default | What |
|---|---|---|
| `KNOWLEDGE_SITE_ROOT` | `~/.local/state/knowledge-tools/site/<instance>` | where the built site is published — bind-mount this into the container |
| `KNOWLEDGE_SITE_BASE_URL` | `example.com` | public host for absolute URLs in RSS/sitemap (navigation is relative, so cosmetic); set to your real host |
| `KNOWLEDGE_SITE_TITLE` | `Knowledge Vault` | the site's page title |
| `KNOWLEDGE_QUARTZ_REF` | `v4.5.2` | pinned Quartz version (a git checkout, not an npm dep) |
| `KNOWLEDGE_SITE_LOG_RETENTION_DAYS` | `30` | prune `vault-site` build logs older than this |

Re-run it whenever the wiki changes to refresh the published site. (A future host-automation pass
will wire systemd units to rebuild after each compile and on a timer; until then, schedule it
yourself or run it on demand.)

## Multiple vaults

This service serves exactly **one** vault (`VAULT_ROOT`). To run several, deploy **one instance
per vault** — a separate container each with its own `VAULT_ROOT`, port, URL, and auth — rather
than one server multiplexing many. That keeps the vaults isolated (filesystem, auth, blast radius)
for free and needs no per-request vault routing.

The only knob for the multi-vault case is a cosmetic label:

```sh
KNOWLEDGE_VAULT_NAME=work     # optional; default unset → single-vault behavior, unchanged
```

When set, it's surfaced in the MCP server name (`knowledge-vault-<slug>`), prepended to the
server instructions, and returned as `vault_name` in `vault_status` / `GET /api/v1/status` — so a
client connected to several vaults can tell them apart. It routes and scopes nothing. Match it to
the connector's server name (`knowledge-vault-<label>`); see the
[vault plugin README](../plugins/vault/README.md#multiple-vaults) for wiring the connectors.

## REST API

The same operations as the MCP tools, as plain JSON HTTP under `/api/v1` — for scripts and
tooling that don't speak MCP. Both surfaces call the same in-process vault core, so behavior is
identical; the REST layer just returns JSON with proper HTTP status codes. It's gated by the
**same optional auth** as `/mcp` (authless behind a proxy by default; `KNOWLEDGE_AUTH_*` validates a
token on every request).

| Method & path | MCP tool | Success |
|---|---|---|
| `GET /api/v1/wiki/search?q=` | `search_wiki` | `200 {query, hits:[{note,snippets}]}` |
| `GET /api/v1/wiki/notes` | `list_notes` | `200 {notes:[...]}` |
| `GET /api/v1/wiki/notes/<path>` | `get_note` | `200 {path, content}` / `404` |
| `GET /api/v1/index` | `list_index` | `200 {content}` |
| `POST /api/v1/inbox` | `append_to_inbox` | `201 {path}` |
| `POST /api/v1/compile` | `compile_run` | `200 {status, available_at?}` |
| `GET /api/v1/status` | `vault_status` | `200` (the `vault_status` JSON) |
| `GET /api/v1/questions?status=` | `list_questions` | `200 {questions:[...]}` |
| `GET /api/v1/questions/<id>` | `get_question` | `200 {id, content}` / `404` |
| `POST /api/v1/questions/<id>/answer` | `answer_question` | `200 {id, status}` |

Notes:
- The note path is taken from the rest of the URL (`/wiki/notes/sub/note.md`); the `.md`
  extension is optional. Paths are confined to the vault — traversal attempts get `400`.
- `POST /inbox` body is `{ "text": "...", "title": "..."? }`; `POST .../answer` body is
  `{ "answer": "..." }`.
- `POST /compile` always returns `200` with a discriminated `status`
  (`triggered` | `empty` | `busy` | `throttled`); `throttled` includes `available_at`. A refused
  manual compile is informational (your captures are safe regardless), so it isn't an error code.
- Errors are JSON `{ "error": "..." }`: `400` for bad/missing input, `404` for a missing
  note/question, `403` for a missing scope (see below), `502` when the review-queue backend
  (GitHub) can't be reached.

```sh
curl -s localhost:3000/api/v1/status
curl -s 'localhost:3000/api/v1/wiki/search?q=networking'
curl -s localhost:3000/api/v1/wiki/notes/some-note
curl -s -XPOST localhost:3000/api/v1/inbox \
  -H 'content-type: application/json' -d '{"text":"a thought","title":"My Note"}'
curl -s -XPOST localhost:3000/api/v1/compile
```

### Auth & least-privilege scopes

`/api/v1` is gated by the same built-in auth as `/mcp`, validated against the **same**
`KNOWLEDGE_AUTH_AUDIENCE` (one identifier for the whole service — see
[Authentication](#authentication)). Pick the OAuth grant by who's calling: an interactive client
uses **authorization_code + PKCE** (the same user token that works on `/mcp` works here), and an
unattended machine uses **client_credentials** (a confidential client mints a service JWT — the
standard M2M grant). Send the token as `Authorization: Bearer …` (or whatever
`KNOWLEDGE_AUTH_TOKEN_HEADER` is set to).

For least-privilege, set **`KNOWLEDGE_API_REQUIRE_SCOPES=true`** and the REST routes require an
OAuth scope per request — **`vault.read`** for the GETs, **`vault.write`** for the writes
(`/inbox`, `/compile`, `/questions/:id/answer`); a missing scope is `403`. So a capture-only cron
gets a `vault.write` token, a read-only dashboard gets `vault.read`. It's **off by default** so
tokens that don't carry these scopes (e.g. an interactive login) keep working; turn it on once
your IdP issues the scopes to the calling client. (Enforced only when built-in auth is on.)

## Authentication

Two models, and you can combine them:

**1. Auth at a proxy (default — server does nothing).** Out of the box the server performs no
auth and trusts its network. Put an authenticating reverse proxy in front of `/mcp` and make
sure the origin can't be reached *around* it (don't publish the container port; keep untrusted
workloads off its network). Portable — bring whatever identity layer you already run.

**2. Built-in token validation (optional).** Set the `KNOWLEDGE_AUTH_*` env and the server becomes an
OAuth 2.1 *resource server*: it validates a JWT access token on every `/mcp` **and `/api/v1`** request
and advertises its authorization server for client discovery (RFC 9728). Vendor-neutral — point it
at any OIDC issuer. It validates tokens but never issues them, so you still need an authorization
server (the issuer). This is what lets the origin protect *itself*, so it's safe even if
something can reach it directly.

```sh
KNOWLEDGE_AUTH_ISSUER=https://your-idp.example.com          # the OIDC issuer (authorization server)
KNOWLEDGE_AUTH_JWKS_URL=https://your-idp.example.com/jwks    # its signing keys
KNOWLEDGE_AUTH_AUDIENCE=https://knowledge.example.com        # expected `aud` claim — see note below
# KNOWLEDGE_AUTH_TOKEN_HEADER=authorization                  # or a proxy-injected JWT header
```

`KNOWLEDGE_AUTH_AUDIENCE` is an opaque identifier the server string-matches the token's `aud`
against — it can be any value, as long as your IdP stamps the same one. Since it now covers **both**
`/mcp` and `/api/v1`, prefer a generic, path-less id for the whole service (e.g.
`https://knowledge.example.com`) rather than a `…/mcp` value. (It's independent of the `/mcp`
discovery document, which keeps advertising the MCP endpoint for connector discovery.)

Set all three to enable; set none to stay authless. (Half-set → the server refuses to start.)

> **Which clients can reach it?** claude.ai's connector fetches **server-side from Anthropic's
> cloud**, so it needs a **publicly reachable** endpoint whose proxy *or* the server's own
> discovery speaks OAuth. The Claude Code CLI runs **on your machine**, so a private network is
> enough for it. Pick a deployment option (below) accordingly.

## Local development

```sh
cd service
npm install
npm run build
cp .env.example .env        # point VAULT_ROOT at a vault; no auth config needed
node --env-file=.env dist/index.js
```

There is no gate locally, so both `/mcp` and `/api/v1` are reachable directly — drive MCP with
the MCP Inspector or curl, and the REST API with curl:

```sh
npx @modelcontextprotocol/inspector        # connect to http://localhost:3000/mcp
curl -s localhost:3000/healthz             # {"ok":true}
curl -s localhost:3000/api/v1/status       # the vault_status JSON
```

## Image (CI)

Pushes to `main` that touch `service/**` trigger `.github/workflows/build-service.yml`, which
builds the image (linux/amd64 + arm64) and pushes it to
**`ghcr.io/josephschmitt/knowledge-service:latest`** (also tagged with the commit SHA).
Deployments pull this published image — they do not build from a source checkout. The package
is **public** (set once in the GHCR package settings after the first push), so the host
pulls without authenticating; the image carries only server code, no vault content.

## Deploying behind auth

Run the container (only required config is `VAULT_ROOT`; add the `KNOWLEDGE_SITE_ROOT` mount +
`KNOWLEDGE_ENABLE_SITE=true` if you want the [static site](#static-website-)), then protect it.
**The service doesn't prescribe an identity layer** — that's yours to choose. Pick whichever of the
two [models](#authentication) suits you, with whatever provider you run; the options below are
illustrative, not requirements.

### Built-in JWT validation (any OIDC issuer)  *(works with claude.ai)*

Set `KNOWLEDGE_AUTH_ISSUER` / `KNOWLEDGE_AUTH_JWKS_URL` / `KNOWLEDGE_AUTH_AUDIENCE` to your
issuer's values; the server validates a token on every request and advertises the issuer for
client discovery (RFC 9728), so the origin protects *itself*. Vendor-neutral — any OIDC issuer
works. For the **claude.ai connector** the issuer must support OAuth dynamic client registration
(DCR), or let you preconfigure claude.ai's client, since claude.ai registers itself on first
connect.

### An auth proxy / gateway (server stays authless)  *(works with claude.ai)*

Put an authenticating reverse proxy or OAuth/OIDC gateway in front and forward only authenticated
requests, leaving the server's own auth off (the default). Use any identity layer you already run.
Two things to get right:

- Make sure the origin can't be reached *around* the proxy (don't publish the container port; keep
  untrusted workloads off its network) — or *also* enable built-in validation so the origin gates
  itself even if something reaches it directly. If the proxy injects the verified identity as a JWT
  header, point `KNOWLEDGE_AUTH_TOKEN_HEADER` at that header and validate it.
- For the **claude.ai connector**, the proxy must serve the discovery claude.ai expects
  (`/.well-known/oauth-protected-resource` + a 401 challenge).

### Private network (no public endpoint)  *(Claude Code only)*

Put the endpoint on a private network (VPN, overlay network, or LAN) and skip a public proxy
entirely — simplest and fully private. **claude.ai can't reach it** (its fetch comes from
Anthropic's cloud), so use this with the **Claude Code** CLI on a machine on the same network: run
`/mcp` to point Claude Code at the private URL.

## Notes

- Vault is mounted read-only except `inbox/` (least privilege — only `append_to_inbox`, the
  compile sentinel, and the files-channel `answer_question` write touch it). The server makes no
  outbound network calls unless the GitHub review channel is configured (see
  [Review channel](#review-channel)).
- DNS-rebinding protection is off by default (the proxy is the gate; claude.ai's fetch may omit
  `Origin`). See `.env.example` to enable it with `ALLOWED_HOSTS`/`ALLOWED_ORIGINS`.
- The server never issues tokens or runs login/MFA/policy — that's the issuer's job. With
  built-in validation it only *verifies* tokens per request; with auth off, even that lives in
  the proxy you deploy it behind.
- Logging is [pino](https://getpino.io) (line-delimited JSON). Default level is `info`; set
  `LOG_LEVEL=debug` to see per-request lines.
