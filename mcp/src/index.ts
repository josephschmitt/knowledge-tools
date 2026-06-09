// Entry point: an Express app serving the Streamable HTTP MCP endpoint. The server does no
// authentication of its own — it trusts the network it is deployed on. Run it behind an
// authenticating reverse proxy and ensure only that proxy can reach it (see mcp/README.md).
import express from 'express';
import { randomUUID } from 'node:crypto';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { isInitializeRequest } from '@modelcontextprotocol/sdk/types.js';
import { buildMcpServer } from './mcp.js';
import { logger } from './logger.js';
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

// One line per request at debug level — cheap, and the first thing you want when a connector
// flow misbehaves (set LOG_LEVEL=debug).
app.use((req, _res, next) => {
  logger.debug({ method: req.method, path: req.path }, 'request');
  next();
});

app.get('/healthz', (_req, res) => {
  res.json({ ok: true });
});

// --- Streamable HTTP MCP endpoint (stateful: one transport+server per session) ---
const transports: Record<string, StreamableHTTPServerTransport> = {};

app.post('/mcp', express.json(), async (req, res) => {
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
app.get('/mcp', replaySession);
app.delete('/mcp', replaySession);

app.listen(PORT, '0.0.0.0', () => {
  logger.info(
    { port: PORT, publicUrl: PUBLIC_URL, mcpEndpoint: `${PUBLIC_URL}/mcp` },
    'knowledge-vault MCP server listening',
  );
});
