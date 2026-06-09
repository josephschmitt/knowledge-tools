# Knowledge Vault MCP Server

A remote [MCP](https://modelcontextprotocol.io) server that exposes this knowledge vault to
**claude.ai as a custom connector**. It serves the **Streamable HTTP** transport and nothing
more — **it performs no authentication of its own**. Authentication is a *deployment* concern:
run it behind an authenticating reverse proxy and make sure only that proxy can reach it.

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

`append_to_inbox` is the capture path: drop a thought from claude.ai, and the scheduled
compile (`scripts/vault-compile.sh`) folds it into the wiki.

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

## Authentication (a deployment concern)

This server does **no** authentication. It is a plain Streamable-HTTP MCP server that trusts
the network it runs on, which keeps the source portable — bring whatever identity layer you
already use. **Your deployment owns two jobs:** put an authenticating proxy in front of `/mcp`,
and make sure the origin can't be reached *around* it (don't publish the container port; keep
untrusted workloads off its network). An exposed origin is an open, writable vault.

> **Which clients can reach it?** claude.ai's connector fetches **server-side from Anthropic's
> cloud**, so it needs a **publicly reachable** endpoint whose proxy speaks OAuth. The Claude
> Code CLI runs **on your machine**, so a private network is enough for it. Pick a deployment
> option (below) accordingly.

## Local development

```sh
cd mcp
npm install
npm run build
cp .env.example .env        # point VAULT_ROOT at a vault; no auth config needed
node --env-file=.env dist/index.js
```

There is no gate locally, so `/mcp` is reachable directly — drive it with the MCP Inspector or
curl:

```sh
npx @modelcontextprotocol/inspector      # connect to http://localhost:3000/mcp
curl -s localhost:3000/healthz           # {"ok":true}
```

## Image (CI)

Pushes to `main` that touch `mcp/**` trigger `.github/workflows/build-mcp.yml`, which builds
the image (linux/amd64 + arm64) and pushes it to
**`ghcr.io/josephschmitt/knowledge-mcp:latest`** (also tagged with the commit SHA). The
homelab pulls this published image — it does not build from a source checkout. The package
is **public** (set once in the GHCR package settings after the first push), so the host
pulls without authenticating; the image carries only server code, no vault content.

## Deploying behind auth

The server's only config is `VAULT_ROOT` (plus optional `PORT` / `PUBLIC_URL` / tuning knobs in
`.env.example`) — there is no auth env. Run the container, then front it with one of these.

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

<details><summary>Homelab specifics (traefik + cloudflared)</summary>

- **Single-level subdomain.** Free Universal SSL covers `example.com` and `*.example.com` only;
  a deeper host like `knowledge.mcp.example.com` has no edge cert. Keep the host at
  `knowledge.example.com` with the endpoint at the `/mcp` path.
- **TLS.** The traefik router requests the origin cert via the `cf-dns` resolver; the public
  edge cert is Universal SSL `*.example.com`.
- **Tunnel.** Add a public hostname `knowledge.example.com` → `https://localhost:443`; set
  cloudflared's **Origin Server Name** to the host (or **No TLS Verify**), since traefik
  terminates TLS on a loopback hop. Adding the hostname creates the DNS record.
- **Start it.** `cd ~/example.com && docker compose pull knowledge-mcp && docker compose up -d knowledge-mcp`
- **Close the bypass.** Don't publish the container port to the host — route to it only on
  traefik's internal network, and keep untrusted workloads off that network. Access guards the
  *public* path only; a LAN client or neighbouring container could otherwise hit the open origin.

</details>

### OIDC gateway (oauth2-proxy / Authelia / Keycloak)  *(public; works with claude.ai)*

Front `/mcp` with a gateway that terminates OAuth/OIDC against your own IdP and forwards only
authenticated requests. For the claude.ai connector the gateway must serve the discovery it
expects (`/.well-known/oauth-protected-resource` + a 401 challenge) and either support dynamic
client registration or let you preconfigure claude.ai's client. Restrict the policy to your
account, then connect claude.ai to the public URL.

### Private network (Tailscale / WireGuard / VPN)  *(Claude Code only)*

Put the endpoint on a private network and skip a public proxy entirely — simplest and fully
private. **claude.ai can't reach it** (its fetch comes from Anthropic's cloud), so use this with
the **Claude Code** CLI on a machine that's on the same tailnet/VPN: run `/mcp` to point Claude
Code at the private URL.

## Notes

- Vault is mounted read-only except `inbox/` (least privilege — only `append_to_inbox` writes).
- DNS-rebinding protection is off by default (the proxy is the gate; claude.ai's fetch may omit
  `Origin`). See `.env.example` to enable it with `ALLOWED_HOSTS`/`ALLOWED_ORIGINS`.
- The server holds no auth state at all — login, MFA, policy, and token lifecycle live entirely
  in whatever proxy you deploy it behind.
- Logging is [pino](https://getpino.io) (line-delimited JSON). Default level is `info`; set
  `LOG_LEVEL=debug` to see per-request lines.
