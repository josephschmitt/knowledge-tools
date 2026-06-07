// Shared pino logger. Level is controlled by LOG_LEVEL (default 'info'); raise it to 'debug'
// to surface per-request and token-verification detail without touching code. Output is
// line-delimited JSON — fine for `docker compose logs` and any log shipper.
import pino from 'pino';
import { LOG_LEVEL } from './config.js';

export const logger = pino({ level: LOG_LEVEL });
