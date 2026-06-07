// Entry point: an Express app that serves OAuth resource-server metadata (pointing at the
// Cloudflare Access OIDC app as the authorization server) and the Streamable HTTP MCP
// endpoint, gated by bearer auth. This server issues no tokens — it only validates the
// Cloudflare-issued token claude.ai forwards (see auth/verifier.ts).
import express from 'express';
import { randomUUID } from 'node:crypto';
import {
  mcpAuthMetadataRouter,
  getOAuthProtectedResourceMetadataUrl,
} from '@modelcontextprotocol/sdk/server/auth/router.js';
import { requireBearerAuth } from '@modelcontextprotocol/sdk/server/auth/middleware/bearerAuth.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { isInitializeRequest } from '@modelcontextprotocol/sdk/types.js';
import type { OAuthMetadata } from '@modelcontextprotocol/sdk/shared/auth.js';
import { buildMcpServer } from './mcp.js';
import { verifier } from './auth/verifier.js';
import { logger } from './logger.js';
import {
  PORT,
  PUBLIC_URL,
  RESOURCE_URL,
  CF_ISSUER,
  CF_JWKS_URL,
  CF_AUTHORIZATION_ENDPOINT,
  CF_TOKEN_ENDPOINT,
  ALLOWED_HOSTS,
  ALLOWED_ORIGINS,
  ENABLE_DNS_REBINDING,
} from './config.js';

const app = express();
app.set('trust proxy', true);
app.disable('x-powered-by');

const resourceMetadataUrl = getOAuthProtectedResourceMetadataUrl(RESOURCE_URL);

// One line per request at debug level — cheap, and the first thing you want when a connector
// flow misbehaves (set LOG_LEVEL=debug).
app.use((req, _res, next) => {
  logger.debug({ method: req.method, path: req.path }, 'request');
  next();
});

app.get('/healthz', (_req, res) => {
  res.json({ ok: true });
});

// Describe the Cloudflare Access OIDC app as our authorization server. mcpAuthMetadataRouter
// serves /.well-known/oauth-protected-resource (pointing claude.ai at Cloudflare to log in)
// and echoes this as /.well-known/oauth-authorization-server.
const oauthMetadata: OAuthMetadata = {
  issuer: CF_ISSUER,
  authorization_endpoint: CF_AUTHORIZATION_ENDPOINT,
  token_endpoint: CF_TOKEN_ENDPOINT,
  jwks_uri: CF_JWKS_URL,
  response_types_supported: ['code'],
  // Cloudflare issues refresh tokens when the app's "Refresh tokens" toggle is on (it does
  // not key off an `offline_access` scope — that scope is absent from its discovery, so we do
  // not advertise it). We list the standard `refresh_token` grant; Cloudflare's own discovery
  // spells it `refresh_tokens`.
  grant_types_supported: ['authorization_code', 'refresh_token'],
  code_challenge_methods_supported: ['S256'],
  scopes_supported: ['openid', 'email', 'profile'],
  token_endpoint_auth_methods_supported: ['client_secret_post', 'client_secret_basic'],
};

app.use(
  mcpAuthMetadataRouter({
    oauthMetadata,
    resourceServerUrl: RESOURCE_URL,
    resourceName: 'Knowledge Vault',
    scopesSupported: ['openid', 'email', 'profile'],
  }),
);

// --- Streamable HTTP MCP endpoint (stateful: one transport+server per session) ---
const transports: Record<string, StreamableHTTPServerTransport> = {};
const bearer = requireBearerAuth({ verifier, resourceMetadataUrl });

app.post('/mcp', bearer, express.json(), async (req, res) => {
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
    res.status(400).json({
      jsonrpc: '2.0',
      error: { code: -32000, message: 'Bad Request: no valid session ID for non-initialize request' },
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
    res.status(400).send('Invalid or missing session ID');
    return;
  }
  await transport.handleRequest(req, res);
};
app.get('/mcp', bearer, replaySession);
app.delete('/mcp', bearer, replaySession);

app.listen(PORT, '0.0.0.0', () => {
  logger.info(
    { port: PORT, publicUrl: PUBLIC_URL, mcpEndpoint: `${PUBLIC_URL}/mcp`, issuer: CF_ISSUER },
    'knowledge-vault MCP server listening',
  );
});
