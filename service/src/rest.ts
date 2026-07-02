// REST API over the vault — the same operations the MCP tools expose (see mcp.ts), as plain
// JSON HTTP so scripts and other tools can reach the vault without speaking MCP. Both surfaces
// call the shared core (vault.ts / review.ts) in-process; this router only maps routes to those
// functions and shapes JSON responses + proper status codes. Mounted under /api/v1 in index.ts,
// behind the same optional `requireToken` gate as /mcp.
import { Router, type ErrorRequestHandler } from 'express';
import {
  listNotes,
  readIndex,
  getNote,
  searchNotes,
  appendToInbox,
  triggerCompile,
  triggerJob,
  getVaultStatus,
  type JobOverrides,
} from './vault.js';
import { listQuestions, getQuestion, answerQuestion } from './review.js';
import { requireScope } from './auth.js';
import { API_SCOPE_READ, API_SCOPE_WRITE } from './config.js';

export const apiRouter = Router();

// Least-privilege: reads (GET/HEAD) need vault.read, writes (POST) need vault.write. Enforced only
// when KNOWLEDGE_API_REQUIRE_SCOPES is on and built-in auth is enabled; otherwise requireScope is a
// no-op. All write routes here are POST and all reads GET, so method is a faithful proxy (Express
// answers HEAD via the GET handlers, so HEAD is read too).
apiRouter.use((req, res, next) =>
  requireScope(
    req.method === 'GET' || req.method === 'HEAD' ? API_SCOPE_READ : API_SCOPE_WRITE,
  )(req, res, next),
);

function errMsg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// A genuine "not found" (vs auth/network/a real failure) is: a missing file (files backend),
// or an absent / unparseable-id GitHub issue (github backend). Mirrors questionError in mcp.ts
// so REST and MCP agree on what counts as 404 vs an upstream error.
function isNotFound(err: unknown): boolean {
  const msg = errMsg(err);
  return (
    (err as NodeJS.ErrnoException)?.code === 'ENOENT' ||
    /\bnot a GitHub issue number\b/.test(msg) ||
    /->\s*404\b/.test(msg)
  );
}

// --- Notes (read) ------------------------------------------------------------------------
// Spans the two queryable areas — library/ (settled) and notebook/ (tentative); tasks/ is
// excluded. These mirror the search_notes / list_notes / get_note MCP tools.

const SEARCH_SCOPES = ['library', 'notebook', 'both'] as const;

apiRouter.get('/search', async (req, res) => {
  const q = (req.query.q ?? '').toString();
  if (!q.trim()) {
    res.status(400).json({ error: 'query parameter "q" is required' });
    return;
  }
  const scope = req.query.scope === undefined ? 'library' : req.query.scope.toString();
  if (!(SEARCH_SCOPES as readonly string[]).includes(scope)) {
    res.status(400).json({ error: `"scope" must be one of: ${SEARCH_SCOPES.join(', ')}` });
    return;
  }
  // Hits carry `area` (library | notebook); `note` stays area-relative — combine them
  // (area/note) for an area-qualified path to feed back to GET /notes/*.
  res.json({ query: q, scope, hits: await searchNotes(q, scope as (typeof SEARCH_SCOPES)[number]) });
});

apiRouter.get('/notes', async (_req, res) => {
  res.json({ notes: await listNotes() });
});

// Notes can live in subdirs, so capture the rest of the path with a named wildcard. confine()
// inside getNote() guards traversal, so an "escapes" error is a 400, a missing file a 404.
apiRouter.get('/notes/*path', async (req, res) => {
  const raw = (req.params as Record<string, unknown>).path;
  const notePath = (Array.isArray(raw) ? raw.join('/') : String(raw ?? '')).trim();
  if (!notePath) {
    res.status(400).json({ error: 'note path is required' });
    return;
  }
  try {
    res.json({ path: notePath, content: await getNote(notePath) });
  } catch (err) {
    if (/escapes the allowed directory/.test(errMsg(err))) {
      res.status(400).json({ error: errMsg(err) });
    } else if (isNotFound(err)) {
      res.status(404).json({ error: `Note not found: ${notePath}` });
    } else {
      // A real filesystem error (EACCES, EIO, ...) is an infrastructure problem, not a
      // missing note — surface it as 500 instead of masking it as a 404.
      res.status(500).json({ error: errMsg(err) });
    }
  }
});

apiRouter.get('/index', async (_req, res) => {
  res.json({ content: await readIndex() });
});

// --- Inbox + compile (write) -------------------------------------------------------------

apiRouter.post('/inbox', async (req, res) => {
  const { text, title } = (req.body ?? {}) as { text?: unknown; title?: unknown };
  if (typeof text !== 'string' || !text.trim()) {
    res.status(400).json({ error: '"text" is required and must be a non-empty string' });
    return;
  }
  if (title !== undefined && typeof title !== 'string') {
    res.status(400).json({ error: '"title" must be a string' });
    return;
  }
  const path = await appendToInbox(text, title);
  res.status(201).json({ path });
});

// Parse the optional per-run model/effort override from a trigger's request body. Each is optional
// and must be a string when present; returns an error message (→ 400) otherwise. Values are
// pass-through / unvalidated (harness-specific), matching the env knobs.
function parseOverrides(body: unknown): { ov: JobOverrides } | { error: string } {
  const { model, effort } = (body ?? {}) as { model?: unknown; effort?: unknown };
  if (model !== undefined && typeof model !== 'string') return { error: '"model" must be a string' };
  if (effort !== undefined && typeof effort !== 'string') return { error: '"effort" must be a string' };
  return { ov: { model, effort } };
}

// Always 200 with a discriminated `status` (triggered | empty | busy | throttled). A refused
// manual compile is informational, not a failure — the captures are safe regardless — so this
// mirrors the MCP tool's semantics rather than returning an error code. Accepts an optional
// { model?, effort? } body to override the run's model/effort (else the host's config/env chain).
apiRouter.post('/compile', async (req, res) => {
  const parsed = parseOverrides(req.body);
  if ('error' in parsed) {
    res.status(400).json({ error: parsed.error });
    return;
  }
  res.json(await triggerCompile(parsed.ov));
});

// The two judgment-call maintenance jobs, mirroring /compile as async on-demand triggers. Always
// 200 with { status: 'triggered' }: the daemon serializes every vault job on the shared lock and
// resolve is a host-side no-op when nothing is answered, so there's no throttle/empty guard to
// report. Poll GET /status (its jobs map) for completion. Accepts the same optional
// { model?, effort? } override body as /compile.
apiRouter.post('/synthesize', async (req, res) => {
  const parsed = parseOverrides(req.body);
  if ('error' in parsed) {
    res.status(400).json({ error: parsed.error });
    return;
  }
  res.json(await triggerJob('synthesize', parsed.ov));
});

apiRouter.post('/resolve', async (req, res) => {
  const parsed = parseOverrides(req.body);
  if ('error' in parsed) {
    res.status(400).json({ error: parsed.error });
    return;
  }
  res.json(await triggerJob('resolve', parsed.ov));
});

apiRouter.get('/status', async (_req, res) => {
  res.json(await getVaultStatus());
});

// --- Judgment-call review queue ----------------------------------------------------------

const QUESTION_STATUSES = ['open', 'answered', 'applied'];

apiRouter.get('/questions', async (req, res) => {
  const status = req.query.status === undefined ? undefined : req.query.status.toString();
  if (status !== undefined && !QUESTION_STATUSES.includes(status)) {
    res.status(400).json({ error: `"status" must be one of: ${QUESTION_STATUSES.join(', ')}` });
    return;
  }
  try {
    res.json({ questions: await listQuestions(status) });
  } catch (err) {
    res.status(502).json({ error: `Could not reach the review queue: ${errMsg(err)}` });
  }
});

apiRouter.get('/questions/:id', async (req, res) => {
  const { id } = req.params;
  try {
    res.json({ id, content: await getQuestion(id) });
  } catch (err) {
    if (isNotFound(err)) res.status(404).json({ error: `Question not found: ${id}` });
    else res.status(502).json({ error: `Could not reach the review queue for ${id}: ${errMsg(err)}` });
  }
});

apiRouter.post('/questions/:id/answer', async (req, res) => {
  const { id } = req.params;
  const { answer } = (req.body ?? {}) as { answer?: unknown };
  if (typeof answer !== 'string' || !answer.trim()) {
    res.status(400).json({ error: '"answer" is required and must be a non-empty string' });
    return;
  }
  try {
    const status = await answerQuestion(id, answer);
    res.json({ id, status });
  } catch (err) {
    if (isNotFound(err)) res.status(404).json({ error: `Question not found: ${id}` });
    else res.status(502).json({ error: `Could not reach the review queue for ${id}: ${errMsg(err)}` });
  }
});

// Anything that slips through (an unexpected throw) becomes a JSON 500 rather than Express's
// default HTML error page, so every /api/v1 response is JSON.
const jsonErrors: ErrorRequestHandler = (err, _req, res, _next) => {
  res.status(500).json({ error: errMsg(err) });
};
apiRouter.use(jsonErrors);
