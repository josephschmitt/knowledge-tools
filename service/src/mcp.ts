// Builds a fresh McpServer with the vault tools. One instance per Streamable HTTP session.
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { z } from 'zod';
import {
  listNotes,
  readIndex,
  getNote,
  searchWiki,
  appendToInbox,
  triggerCompile,
  getVaultStatus,
} from './vault.js';
import { listQuestions, getQuestion, answerQuestion } from './review.js';
import { VAULT_NAME } from './config.js';

function fmtWhen(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const mins = Math.ceil((t - Date.now()) / 60000);
  if (mins <= 0) return 'now';
  if (mins < 60) return `in ~${mins} min`;
  return `in ~${Math.ceil(mins / 60)} h`;
}

function text(s: string) {
  return { content: [{ type: 'text' as const, text: s }] };
}

// A review-tool error is genuinely "no such question" only when the file is missing (files
// backend) or the issue is absent / its id unparseable (GitHub backend). Anything else — auth,
// rate-limit, network, a partially-applied write — is real and must surface verbatim, not be
// flattened into "Question not found" (which invites a destructive re-answer).
function questionError(id: string, err: unknown) {
  const msg = err instanceof Error ? err.message : String(err);
  const notFound =
    (err as NodeJS.ErrnoException)?.code === 'ENOENT' ||
    /\bnot a GitHub issue number\b/.test(msg) ||
    /->\s*404\b/.test(msg);
  return text(notFound ? `Question not found: ${id}` : `Could not reach the review queue for ${id}: ${msg}`);
}

/** Slug of the vault label for the MCP server name (keeps it token-safe). '' when unlabeled. */
function nameSlug(): string {
  const s = VAULT_NAME.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  return s ? `-${s}` : '';
}

export function buildMcpServer(): McpServer {
  const server = new McpServer(
    // Suffix the protocol-stable base name with the vault label so a client connected to several
    // vaults can tell the servers apart; unlabeled deployments stay exactly 'knowledge-vault'.
    { name: `knowledge-vault${nameSlug()}`, version: '0.1.0' },
    {
      instructions:
        (VAULT_NAME ? `This vault is "${VAULT_NAME}". ` : '') +
        'Personal knowledge vault, split on purpose: capture is dumb, compilation is smart. ' +
        'Answer questions from the compiled wiki (search_wiki / get_note / list_index / ' +
        'list_notes), preferring it over general knowledge. Save material — knowledge or ' +
        'tasks — raw with append_to_inbox; a scheduled compiler curates the inbox into the wiki. ' +
        'compile_run / vault_status trigger and track that compile. When that maintenance ' +
        'hits a judgment call it can\'t decide alone, list_questions / get_question surface ' +
        'it and answer_question records my decision for the next pass to apply.',
    },
  );

  server.registerTool(
    'search_wiki',
    {
      title: 'Search the wiki',
      description:
        'Case-insensitive substring search across all compiled wiki notes (searches wiki/ only, ' +
        'not tasks/). Returns matching note paths with snippets; pass a path to get_note to read a full note.',
      inputSchema: { query: z.string().min(1).describe('Text to search for') },
    },
    async ({ query }) => {
      const hits = await searchWiki(query);
      if (hits.length === 0) return text(`No wiki notes match "${query}".`);
      const out = hits
        .map((h) => `## ${h.note}\n${h.snippets.map((s) => `> ${s.replace(/\n/g, '\n> ')}`).join('\n\n')}`)
        .join('\n\n');
      return text(`${hits.length} match(es) for "${query}":\n\n${out}`);
    },
  );

  server.registerTool(
    'get_note',
    {
      title: 'Get a note',
      description: "Return the full markdown of one wiki note by its path or name (e.g. 'homelab-infrastructure').",
      inputSchema: { path: z.string().min(1).describe("Note path or name relative to wiki/, with or without .md") },
    },
    async ({ path: notePath }) => {
      try {
        return text(await getNote(notePath));
      } catch {
        return text(`Note not found: ${notePath}`);
      }
    },
  );

  server.registerTool(
    'list_index',
    {
      title: 'Read the index',
      description: 'Return index.md — the navigation map of the wiki.',
      inputSchema: {},
    },
    async () => text(await readIndex()),
  );

  server.registerTool(
    'list_notes',
    {
      title: 'List notes',
      description: 'List every wiki note by its path. Useful when a search comes up empty.',
      inputSchema: {},
    },
    async () => {
      const notes = await listNotes();
      return text(notes.length ? notes.join('\n') : '(no notes yet)');
    },
  );

  server.registerTool(
    'append_to_inbox',
    {
      title: 'Capture to inbox',
      description:
        'Append a raw capture (a thought, link, or clipping) to the vault inbox. Capture takes ' +
        'zero decisions: do NOT search the wiki for duplicates first and do NOT judge whether ' +
        'the item is worth keeping — dedup and curation happen at compile time. When in doubt, ' +
        'capture. A capture may be knowledge or a task; for an action, lead the text with TODO: ' +
        'and include any deadline so the compiler files it as actionable.',
      inputSchema: {
        text: z
          .string()
          .min(1)
          .describe(
            'The raw capture content. There are no separate source/context fields — fold a ' +
              'source URL and a line of context into the text.',
          ),
        title: z.string().optional().describe('Optional short title'),
      },
    },
    async ({ text: body, title }) => {
      const rel = await appendToInbox(body, title);
      return text(`Captured to ${rel}. It will be compiled into the wiki on the next scheduled compile.`);
    },
  );

  server.registerTool(
    'compile_run',
    {
      title: 'Trigger a compile',
      description:
        'Request an on-demand compile of the inbox into the wiki. Asynchronous: returns ' +
        'immediately — poll vault_status to see when it finishes. Rate-limited to one manual ' +
        'compile per hour. Only needed to process the inbox sooner than the next scheduled ' +
        'compile; capturing alone does not require it.',
      inputSchema: {},
    },
    async () => {
      const result = await triggerCompile();
      switch (result.status) {
        case 'empty':
          return text('Inbox is empty — nothing to compile. (No cooldown consumed.)');
        case 'busy':
          return text('A compile is already running. Your captures are safe; no need to trigger another.');
        case 'throttled':
          return text(
            `Refused — a manual compile ran recently and the hourly cooldown is still active. ` +
              `Next manual compile available ${fmtWhen(result.available_at)}. Your captures are safe ` +
              `in the inbox; the scheduled compile will process them regardless.`,
          );
        case 'triggered':
          return text(
            'Compile triggered. It runs on the home server and the wiki updates once it finishes; ' +
              'your captures are safe in the inbox until then.',
          );
      }
    },
  );

  server.registerTool(
    'vault_status',
    {
      title: 'Vault status',
      description:
        'Pollable vault status as JSON: vault_name (this vault\'s label, or null — use this ' +
        'to tell apart several connected vaults), last_compiled_at (when the most recent compile ' +
        '*finished* — newer than your compile_run trigger time means that run is done), ' +
        'pending_inbox_count, manual_compile_available_at (when the next manual compile_run ' +
        'is allowed; null/past = now), and running. Cheap to poll for a compile to finish.',
      inputSchema: {},
    },
    async () => text(JSON.stringify(await getVaultStatus(), null, 2)),
  );

  server.registerTool(
    'list_questions',
    {
      title: 'List judgment calls',
      description:
        'List the judgment-call questions the vault is waiting on me to decide — contradictions ' +
        'it found between notes, or claims it cannot verify internally. Optionally filter by ' +
        "status ('open' = awaiting my answer, 'answered' = decided but not yet applied, " +
        "'applied' = done). Use this to see what the vault needs from me, then answer_question.",
      inputSchema: {
        status: z
          .enum(['open', 'answered', 'applied'])
          .optional()
          .describe('Filter by status; omit to list all'),
      },
    },
    async ({ status }) => {
      let qs;
      try {
        qs = await listQuestions(status);
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        return text(`Could not reach the review queue: ${msg}`);
      }
      if (qs.length === 0) {
        return text(status ? `No ${status} questions.` : 'No questions in the review queue.');
      }
      const out = qs
        .map((q) => `- [${q.status}] ${q.id} (${q.kind}) — ${q.title}`)
        .join('\n');
      return text(`${qs.length} question(s):\n\n${out}\n\nUse get_question <id> for the full context.`);
    },
  );

  server.registerTool(
    'get_question',
    {
      title: 'Get a judgment call',
      description:
        'Return the full markdown of one review-queue question by its id (from list_questions): ' +
        'the contradiction or claim, the notes involved, and any prior discussion.',
      inputSchema: { id: z.string().min(1).describe('Question id from list_questions') },
    },
    async ({ id }) => {
      try {
        return text(await getQuestion(id));
      } catch (err) {
        return questionError(id, err);
      }
    },
  );

  server.registerTool(
    'answer_question',
    {
      title: 'Answer a judgment call',
      description:
        'Record my decision on a review-queue question. Writes the answer and marks it answered ' +
        'so the next maintenance pass applies it to the wiki and closes it out. If my answer is ' +
        'ambiguous the maintenance pass will come back with a sharper follow-up question.',
      inputSchema: {
        id: z.string().min(1).describe('Question id from list_questions'),
        answer: z.string().min(1).describe('My decision, in my own words'),
      },
    },
    async ({ id, answer }) => {
      try {
        await answerQuestion(id, answer);
      } catch (err) {
        return questionError(id, err);
      }
      return text(
        `Answer recorded for ${id} and marked answered. The next maintenance pass will apply it ` +
          `to the wiki (or follow up here if anything is unclear).`,
      );
    },
  );

  return server;
}
