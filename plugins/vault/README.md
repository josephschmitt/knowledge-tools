# vault plugin

The **`knowledge-vault`** skill and its MCP connector — capture knowledge and tasks into a
personal LLM wiki and query the compiled notes, all through the vault's MCP server. (See the
repo [`README.md`](../../README.md) for the full project and the optional `auto-capture` plugin.)

## Install

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install vault@knowledge-tools
```

On enable, the plugin prompts for your **Knowledge vault MCP URL** — the `/mcp` endpoint
(e.g. `https://knowledge.example.com/mcp`) — and wires it up as a remote HTTP MCP server named
`knowledge-vault`. You must deploy the vault service yourself and reach it at that URL; see
[`service/README.md`](../../service/README.md).

## Authentication

OAuth is negotiated automatically on first connect against whatever proxy/IdP fronts your
endpoint — run `/mcp` to authenticate. As long as the authorization server supports **Dynamic
Client Registration (DCR)** (e.g. Cloudflare Access Managed OAuth), Claude Code registers a
client on the fly and there's nothing else to configure.

### If your IdP doesn't support DCR — set the client ID via `.mcp.json`

Some self-hosted IdPs (e.g. **Authelia**) have no DCR, so Claude Code can't self-register and
fails with *"Incompatible auth server: does not support dynamic client registration."*

**This plugin cannot supply the client ID for you.** Claude Code interpolates plugin config into
a server's `url` but **not** into its `oauth` block, so a client ID set through the plugin reaches
the IdP as the literal string `${user_config.oauth_client_id}` and is rejected. Instead, define
the server in a **`.mcp.json`** with a *literal* client ID — a `.mcp.json` entry **overrides** the
plugin's same-named server, and `~/.mcp.json` applies to every project (Claude reads `.mcp.json`
from the working directory up to the filesystem root):

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

1. Pre-register a **public + PKCE** client (no secret) in your IdP. Register **both**
   `http://127.0.0.1:47832/callback` and `http://localhost:47832/callback` as redirect URIs
   (IdPs match exactly and native OAuth clients disagree on the loopback host); the `callbackPort`
   above must match.
2. Add the `.mcp.json` entry, reload, and approve the `knowledge-vault` server if Claude prompts
   (or set `enableAllProjectMcpServers` / `enabledMcpjsonServers` in `settings.json`).
3. `/mcp` → **Authenticate**.

DCR deployments need none of this — leave the plugin's bare server alone and it auto-registers.

## Multiple vaults

Multi-vault here means **multiple deployments, not one server multiplexing many vaults**: each
vault is its own service (its own `VAULT_ROOT`, URL, and auth) connected as its own MCP server. The
plugin connects exactly one (`knowledge-vault`, from the URL you enter on install) — add the others
in `.mcp.json` under distinct names following the convention **`knowledge-vault-<label>`**:

```json
{
  "mcpServers": {
    "knowledge-vault": {
      "type": "http",
      "url": "https://personal.knowledge.example.com/mcp"
    },
    "knowledge-vault-work": {
      "type": "http",
      "url": "https://work.knowledge.example.com/mcp",
      "oauth": { "clientId": "<work-public-client-id>", "callbackPort": 47832 }
    }
  }
}
```

Notes:

- **The bare `knowledge-vault` server is the default** — the target for `auto-capture` and for any
  capture/recall where you don't name a vault. Keep your primary vault as `knowledge-vault` and add
  the rest as `knowledge-vault-<label>`. The skill asks "which vault?" only when more than one is
  connected *and* you didn't name one.
- Set each service's **`KNOWLEDGE_VAULT_NAME`** to its `<label>` (see
  [`service/README.md`](../../service/README.md)) so the label the skill reads back from
  `vault_status` matches the connector name.
- Auth is per server: a DCR proxy needs nothing; a no-DCR IdP gets its own literal `oauth.clientId`
  per entry (as above). Each `knowledge-vault-*` entry overrides/extends the plugin's single server.
