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
  console.log(`knowledge-vault MCP server listening on :${PORT}`);
  console.log(`  public URL: ${PUBLIC_URL}`);
  console.log(`  MCP endpoint: ${PUBLIC_URL}/mcp`);
  console.log(`  auth: Cloudflare Access OIDC (${CF_ISSUER})`);
});
