// Shared pino logger. Level is controlled by LOG_LEVEL (default 'info'); raise it to 'debug'
// to surface per-request and token-verification detail without touching code. Output is
// line-delimited JSON — fine for `docker compose logs` and any log shipper.
//
// Logs go to STDERR (fd 2), not stdout: the stdio entrypoint (src/stdio.ts) uses stdout as the
// MCP JSON-RPC framing channel, and a single log line on fd 1 would corrupt it. stderr is the
// conventional log stream and `docker compose logs` captures both, so the HTTP deployment is
// unaffected.
import pino from 'pino';
import { LOG_LEVEL } from './config.js';

export const logger = pino({ level: LOG_LEVEL }, pino.destination(2));
