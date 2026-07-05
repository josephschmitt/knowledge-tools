#!/usr/bin/env node
// Local stdio entrypoint: the same MCP server (buildMcpServer) as the HTTP deployment, but over the
// stdio transport so it runs via `npx @joe-sh/knowledge-tools-mcp` under Cowork / Claude Desktop —
// no Express, no Docker, no host daemon. It runs the server in agent-driven mode (passed explicitly
// below), so the three trigger tools return the vault's own procedure for the calling agent to run
// itself and a scheduled Cowork task replaces the daemon.
//
// stdout is the JSON-RPC framing channel — nothing must write to it but the transport. Nothing on
// this import graph constructs the pino logger (only index.ts / auth.ts do, and neither is imported
// here), so there are no logs to keep off stdout; the transport is the sole writer.
import { promises as fs } from 'node:fs';
import { VAULT_ROOT } from './config.js';
import { buildMcpServer } from './mcp.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';

// A VAULT_ROOT that isn't a real directory (e.g. the container default `/vault` on a laptop) would
// otherwise surface only as empty search results — fail fast with a clear message on stderr.
try {
  const st = await fs.stat(VAULT_ROOT);
  if (!st.isDirectory()) throw new Error('not a directory');
} catch {
  process.stderr.write(
    `FATAL: VAULT_ROOT (${VAULT_ROOT}) is not a readable directory. Set VAULT_ROOT to your vault ` +
      `path (seed one with \`knowledge-tools init\`).\n`,
  );
  process.exit(1);
}

// Agent-driven is passed explicitly (not via env) so it can't be silently dropped by import timing.
await buildMcpServer({ agentDriven: true }).connect(new StdioServerTransport());
