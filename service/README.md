# Knowledge Vault Service

One server that exposes this knowledge vault over **two protocols**, both backed by the same
in-process vault core:

- **MCP** at `/mcp` — a remote [MCP](https://modelcontextprotocol.io) endpoint over the
  **Streamable HTTP** transport, used by **claude.ai as a custom connector** (and the Claude
  Code plugin). The MCP *protocol* server name is `knowledge-vault`.
- **REST** at `/api/v1` — a plain JSON HTTP API mirroring the MCP tools 1:1, for scripts,
  automation, and any other tooling that doesn't speak MCP. See [REST API](#rest-api).

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
(`scripts/knowledge-compile.path.in`) watches to start the same `knowledge-compile.service`
the scheduled timer uses — so systemd runs one compile at a time (the shared lock). The
host writes `inbox/.compile/status.json`, which the server reads to return `triggered` /
`throttled` (refused within the one-hour cooldown) / `busy` / `empty`. The scheduled
run is never throttled and doesn't consume the manual cooldown.

Because `compile_run` returns before the compile finishes, `vault_status` is the completion
signal: the host records `last_compiled_at` at the *end* of every successful compile (both
scheduled and on-demand), so a `last_compiled_at` newer than your trigger time means the run
finished. It also reports `pending_inbox_count` and `manual_compile_available_at` (when the
cooldown next clears) — poll it after a `compile_run` to know when the wiki is caught up.

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
  note/question, `502` when the review-queue backend (GitHub) can't be reached.

```sh
curl -s localhost:3000/api/v1/status
curl -s 'localhost:3000/api/v1/wiki/search?q=homelab'
curl -s localhost:3000/api/v1/wiki/notes/homelab-infrastructure
curl -s -XPOST localhost:3000/api/v1/inbox \
  -H 'content-type: application/json' -d '{"text":"a thought","title":"My Note"}'
curl -s -XPOST localhost:3000/api/v1/compile
```

## Authentication

Two models, and you can combine them:

**1. Auth at a proxy (default — server does nothing).** Out of the box the server performs no
auth and trusts its network. Put an authenticating reverse proxy in front of `/mcp` and make
sure the origin can't be reached *around* it (don't publish the container port; keep untrusted
workloads off its network). Portable — bring whatever identity layer you already run.

**2. Built-in token validation (optional).** Set the `KNOWLEDGE_AUTH_*` env and the server becomes an
OAuth 2.1 *resource server*: it validates a JWT access token on every `/mcp` request and
advertises its authorization server for client discovery (RFC 9728). Vendor-neutral — point it
at any OIDC issuer. It validates tokens but never issues them, so you still need an authorization
server (the issuer). This is what lets the origin protect *itself*, so it's safe even if
something can reach it directly.

```sh
KNOWLEDGE_AUTH_ISSUER=https://your-idp.example.com          # the OIDC issuer (authorization server)
KNOWLEDGE_AUTH_JWKS_URL=https://your-idp.example.com/jwks    # its signing keys
KNOWLEDGE_AUTH_AUDIENCE=https://knowledge.example.com/mcp    # expected `aud` claim
# KNOWLEDGE_AUTH_TOKEN_HEADER=authorization                  # or e.g. cf-access-jwt-assertion
```

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
**`ghcr.io/josephschmitt/knowledge-service:latest`** (also tagged with the commit SHA). The
homelab pulls this published image — it does not build from a source checkout. The package
is **public** (set once in the GHCR package settings after the first push), so the host
pulls without authenticating; the image carries only server code, no vault content.

## Deploying behind auth

Run the container (only required config is `VAULT_ROOT`), then choose how it's protected.

### Cloudflare Access + Managed OAuth  *(public; works with claude.ai)*

Cloudflare runs the whole OAuth flow at the edge and forwards only authenticated requests. This
is how the homelab runs it.

1. Expose the container at a public hostname through a reverse proxy / tunnel (e.g. traefik + a
   Cloudflare Tunnel public hostname → `knowledge.example.com`). Keep the MCP port internal.
2. **Zero Trust → Access → Applications → Add → Self-hosted**: hostname `knowledge.example.com`,
   path `/mcp`. Enable **Managed OAuth**, and add a **policy** allowing only your identity (email).
3. claude.ai → **Settings → Connectors → Add custom connector** → `https://knowledge.example.com/mcp`.
   Managed OAuth serves discovery + dynamic registration, so there are no client credentials to
   paste — complete the Cloudflare login + consent and the tools appear.

**Recommended: also turn on built-in validation.** Cloudflare injects a `Cf-Access-Jwt-Assertion`
JWT on every request it forwards. Point the server's built-in auth at it so the origin protects
*itself* — then you don't need network isolation to stop LAN/sibling-container access:

```sh
KNOWLEDGE_AUTH_ISSUER=https://<team>.cloudflareaccess.com
KNOWLEDGE_AUTH_JWKS_URL=https://<team>.cloudflareaccess.com/cdn-cgi/access/certs
KNOWLEDGE_AUTH_AUDIENCE=<your Access application's AUD tag>
KNOWLEDGE_AUTH_TOKEN_HEADER=cf-access-jwt-assertion
```

<details><summary>Homelab specifics (traefik + cloudflared)</summary>

- **Single-level subdomain.** Free Universal SSL covers `example.com` and `*.example.com` only;
  a deeper host like `knowledge.mcp.example.com` has no edge cert. Keep the host at
  `knowledge.example.com` with the endpoint at the `/mcp` path.
- **TLS.** The traefik router requests the origin cert via the `cf-dns` resolver; the public
  edge cert is Universal SSL `*.example.com`.
- **Tunnel.** Add a public hostname `knowledge.example.com` → `https://localhost:443`; set
  cloudflared's **Origin Server Name** to the host (or **No TLS Verify**), since traefik
  terminates TLS on a loopback hop. Adding the hostname creates the DNS record.
- **Start it.** `cd ~/example.com && docker compose pull knowledge-service && docker compose up -d knowledge-service`
- **The bypass.** Without built-in validation, Access guards only the *public* path — a LAN
  client or neighbouring container could hit the origin directly. Either enable built-in
  validation (above) or keep the port off the host and the origin off shared networks.

</details>

### Your own OIDC issuer (Auth0 / Keycloak / Authentik / oauth2-proxy)  *(public; works with claude.ai)*

Two ways to use your own IdP:

- **Built-in validation** — set `KNOWLEDGE_AUTH_ISSUER`/`KNOWLEDGE_AUTH_JWKS_URL`/`KNOWLEDGE_AUTH_AUDIENCE` to the
  IdP's values; the server validates tokens and advertises the issuer for discovery itself. For
  the claude.ai connector the IdP must support dynamic client registration (Auth0/Keycloak do)
  or let you preconfigure claude.ai's client.
- **A gateway** (oauth2-proxy / Authelia) terminating OAuth/OIDC in front of `/mcp` and
  forwarding only authenticated requests — leave the server authless behind it. The gateway must
  serve the discovery claude.ai expects (`/.well-known/oauth-protected-resource` + a 401 challenge).

Either way, restrict the policy to your account.

### Private network (Tailscale / WireGuard / VPN)  *(Claude Code only)*

Put the endpoint on a private network and skip a public proxy entirely — simplest and fully
private. **claude.ai can't reach it** (its fetch comes from Anthropic's cloud), so use this with
the **Claude Code** CLI on a machine that's on the same tailnet/VPN: run `/mcp` to point Claude
Code at the private URL.

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
