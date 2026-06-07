// Builds a fresh McpServer with the vault tools. One instance per Streamable HTTP session.
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { z } from 'zod';
import { listNotes, readIndex, getNote, searchWiki, appendToInbox } from './vault.js';

function text(s: string) {
  return { content: [{ type: 'text' as const, text: s }] };
}

export function buildMcpServer(): McpServer {
  const server = new McpServer(
    { name: 'knowledge-vault', version: '0.1.0' },
    {
      instructions:
        'Personal knowledge vault. Use search_wiki / get_note / list_index / list_notes to ' +
        'answer from the compiled wiki, and append_to_inbox to capture a raw note or idea for ' +
        'later compilation. Prefer answering from the vault over general knowledge.',
    },
  );

  server.registerTool(
    'search_wiki',
    {
      title: 'Search the wiki',
      description: 'Case-insensitive full-text search across all compiled wiki notes. Returns matching notes with snippets.',
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
      description: 'List every wiki note by its path.',
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
        'Append a raw capture (a thought, link, or clipping) to the vault inbox. The nightly ' +
        'compile turns inbox captures into durable wiki notes. Use this to save something for later.',
      inputSchema: {
        text: z.string().min(1).describe('The raw capture content'),
        title: z.string().optional().describe('Optional short title'),
      },
    },
    async ({ text: body, title }) => {
      const rel = await appendToInbox(body, title);
      return text(`Captured to ${rel}. It will be compiled into the wiki on the next nightly run.`);
    },
  );

  return server;
}
