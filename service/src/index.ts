// Entry point: an Express app serving the vault over two protocols — a Streamable HTTP MCP
// endpoint (/mcp) and a REST API (/api/v1) — both backed by the same in-process vault core.
// Authentication is optional and OFF by default — the server trusts the network it is deployed
// on, so run it behind an authenticating proxy. Set KNOWLEDGE_AUTH_* to make it validate tokens
// itself (see auth.ts and service/README.md).
import express from 'express';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { isInitializeRequest } from '@modelcontextprotocol/sdk/types.js';
import { buildMcpServer } from './mcp.js';
import { apiRouter } from './rest.js';
import { requireToken, mountAuthMetadata } from './auth.js';
import { logger } from './logger.js';
import {
  PORT,
  PUBLIC_URL,
  ALLOWED_HOSTS,
  ALLOWED_ORIGINS,
  ENABLE_DNS_REBINDING,
  ENABLE_MCP,
  ENABLE_REST,
  ENABLE_SITE,
  SITE_ROOT,
} from './config.js';

const app = express();
app.set('trust proxy', true);
app.disable('x-powered-by');

// One line per request at debug level — cheap, and the first thing you want when a connector
// flow misbehaves (set LOG_LEVEL=debug).
app.use((req, _res, next) => {
  logger.debug({ method: req.method, path: req.path }, 'request');
  next();
});

app.get('/healthz', (_req, res) => {
  res.json({ ok: true });
});

// Advertise the authorization server (RFC 9728 discovery) for any client that auto-discovers it.
// Served whenever built-in auth is on (no-op otherwise), independent of which surface is enabled —
// a REST-only deployment with auth still needs to point clients at the issuer. (The advertised
// resource id is `${PUBLIC_URL}/mcp`; auth uses one shared audience across both surfaces.)
mountAuthMetadata(app);

// --- REST API (/api/v1) — the same vault operations as the MCP tools, as plain JSON. ---
// Gated by the same `requireToken` (and audience) as /mcp; pass-through when auth is disabled.
// Per-route least-privilege scope checks (vault.read/vault.write) live in rest.ts.
if (ENABLE_REST) {
  app.use('/api/v1', requireToken, express.json(), apiRouter);
}

// --- Streamable HTTP MCP endpoint (stateful: one transport+server per session) ---
// `requireToken` gates every /mcp route; it is a pass-through when auth is disabled. The routes
// are mounted only when MCP is enabled (discovery metadata is handled above, for either surface).
if (ENABLE_MCP) {
  const transports: Record<string, StreamableHTTPServerTransport> = {};

  app.post('/mcp', requireToken, express.json(), async (req, res) => {
    const sid = req.headers['mcp-session-id'] as string | undefined;
    logger.debug({ sessionId: sid, rpcMethod: req.body?.method }, 'mcp request');
    let transport = sid ? transports[sid] : undefined;

    if (!transport && !sid && isInitializeRequest(req.body)) {
      transport = new StreamableHTTPServerTransport({
        sessionIdGenerator: () => randomUUID(),
        enableDnsRebindingProtection: ENABLE_DNS_REBINDING,
        allowedHosts: ALLOWED_HOSTS,
        allowedOrigins: ALLOWED_ORIGINS,
        onsessioninitialized: (id) => {
          transports[id] = transport!;
        },
      });
      transport.onclose = () => {
        if (transport!.sessionId) delete transports[transport!.sessionId];
      };
      await buildMcpServer().connect(transport);
    } else if (!transport) {
      // Unknown/expired session (e.g. after a redeploy wiped the in-memory `transports` map):
      // answer 404, not 400. The MCP Streamable-HTTP spec says a client that gets 404 for a
      // request carrying an Mcp-Session-Id MUST transparently re-initialize. A 400 reads as a
      // hard failure and forces a manual reconnect in claude.ai.
      res.status(404).json({
        jsonrpc: '2.0',
        error: { code: -32001, message: 'Session not found' },
        id: null,
      });
      return;
    }

    await transport.handleRequest(req, res, req.body);
  });

  // GET (server->client SSE) and DELETE (session teardown) reuse the existing session.
  const replaySession: express.RequestHandler = async (req, res) => {
    const sid = req.headers['mcp-session-id'] as string | undefined;
    const transport = sid ? transports[sid] : undefined;
    if (!transport) {
      // 404 (not 400) so a stale session ID — e.g. left over after a redeploy — triggers a
      // clean client-side re-initialize rather than a manual reconnect.
      res.status(404).send('Session not found');
      return;
    }
    await transport.handleRequest(req, res);
  };
  app.get('/mcp', requireToken, replaySession);
  app.delete('/mcp', requireToken, replaySession);
}

// --- Static website (/) — a pre-built Quartz rendering of the library. ---
// Mounted LAST so /healthz, the auth metadata, /api/v1 and /mcp (all registered above) win; this
// is the catch-all. Gated by the same `requireToken` as the other surfaces. The host's
// vault-site.sh builds the artifact into SITE_ROOT; the dir may not exist yet at startup (the
// host populates it asynchronously), so warn but mount anyway — express.static picks up files as
// they appear.
if (ENABLE_SITE) {
  if (!existsSync(SITE_ROOT)) {
    logger.warn(
      { siteRoot: SITE_ROOT },
      'site root not present yet — serving will start once the host build populates it',
    );
  }
  // A single express.static serves both Quartz output shapes: directory `index.html` is auto-served,
  // and `extensions: ['html']` resolves Quartz's clean URLs (/library/foo -> library/foo.html). Default
  // `dotfiles: 'ignore'` and root-confined resolution handle dotfile/traversal safety — leave them.
  app.use(requireToken, express.static(SITE_ROOT, { extensions: ['html'] }));
  // Fall through to Quartz's generated 404.html for unmatched GET/HEAD requests (with a 404 status,
  // not 200). Scoped to GET/HEAD, and skips the /api and /mcp surfaces so an unknown API/MCP path
  // isn't answered with the library's HTML 404 page — those fall through to Express's default handler.
  const notFoundPage = path.join(SITE_ROOT, '404.html');
  app.use(requireToken, (req, res, next) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') return next();
    if (req.path.startsWith('/api/') || req.path === '/mcp' || req.path.startsWith('/mcp/')) return next();
    res.status(404).sendFile(notFoundPage, (err) => {
      if (err) next();
    });
  });
}

app.listen(PORT, '0.0.0.0', () => {
  logger.info(
    {
      port: PORT,
      publicUrl: PUBLIC_URL,
      mcpEndpoint: ENABLE_MCP ? `${PUBLIC_URL}/mcp` : null,
      restEndpoint: ENABLE_REST ? `${PUBLIC_URL}/api/v1` : null,
      siteEndpoint: ENABLE_SITE ? `${PUBLIC_URL}/` : null,
    },
    'knowledge-service listening',
  );
});
