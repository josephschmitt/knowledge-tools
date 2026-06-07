// Entry point: an Express app that serves the OAuth endpoints (via the SDK auth router),
// the single-user approval page, and the Streamable HTTP MCP endpoint gated by bearer auth.
import express from 'express';
import { randomUUID } from 'node:crypto';
import { mcpAuthRouter, getOAuthProtectedResourceMetadataUrl } from '@modelcontextprotocol/sdk/server/auth/router.js';
import { requireBearerAuth } from '@modelcontextprotocol/sdk/server/auth/middleware/bearerAuth.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { isInitializeRequest } from '@modelcontextprotocol/sdk/types.js';
import { buildMcpServer } from './mcp.js';
import { provider, issueAuthorizationCode, checkPassphrase } from './auth/provider.js';
import { loginPage } from './auth/login.js';
import * as store from './auth/store.js';
import {
  PORT,
  PUBLIC_URL,
  ALLOWED_HOSTS,
  ALLOWED_ORIGINS,
  ENABLE_DNS_REBINDING,
} from './config.js';

const app = express();
app.set('trust proxy', true);
app.disable('x-powered-by');

const issuerUrl = new URL(PUBLIC_URL);
const resourceUrl = new URL(`${PUBLIC_URL}/mcp`);
const resourceMetadataUrl = getOAuthProtectedResourceMetadataUrl(resourceUrl);

app.get('/healthz', (_req, res) => {
  res.json({ ok: true });
});

// Approval form target. Validates client + redirect_uri + passphrase, then issues the code
// and redirects back to the client (claude.ai). Has its own body parser per SDK convention.
app.post('/approve', express.urlencoded({ extended: false }), (req, res) => {
  const body = req.body as Record<string, string | undefined>;
  const clientId = body.client_id;
  const redirectUri = body.redirect_uri;
  const codeChallenge = body.code_challenge;
  const state = body.state;
  const scope = body.scope ?? '';
  const resource = body.resource || undefined;

  const client = clientId ? store.getClient(clientId) : undefined;
  if (!client || !redirectUri || !codeChallenge) {
    res.status(400).send('Invalid authorization request');
    return;
  }
  if (!client.redirect_uris.includes(redirectUri)) {
    res.status(400).send('Unregistered redirect_uri');
    return;
  }
  if (!checkPassphrase(body.passphrase ?? '')) {
    res.status(401).setHeader('Content-Type', 'text/html; charset=utf-8');
    res.send(
      loginPage(
        clientId!,
        {
          redirectUri,
          codeChallenge,
          state,
          scopes: scope ? scope.split(' ').filter(Boolean) : [],
          resource: resource ? new URL(resource) : undefined,
        },
        { error: true },
      ),
    );
    return;
  }

  const code = issueAuthorizationCode({
    clientId: clientId!,
    redirectUri,
    codeChallenge,
    scopes: scope ? scope.split(' ').filter(Boolean) : [],
    resource,
  });
  const url = new URL(redirectUri);
  url.searchParams.set('code', code);
  if (state) url.searchParams.set('state', state);
  res.redirect(url.toString());
});

// OAuth metadata + DCR + authorize + token + revoke. Mounted at the app root.
app.use(
  mcpAuthRouter({
    provider,
    issuerUrl,
    resourceServerUrl: resourceUrl,
    resourceName: 'Knowledge Vault',
    scopesSupported: ['vault'],
  }),
);

// --- Streamable HTTP MCP endpoint (stateful: one transport+server per session) ---
const transports: Record<string, StreamableHTTPServerTransport> = {};
const bearer = requireBearerAuth({ verifier: provider, resourceMetadataUrl });

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
});
