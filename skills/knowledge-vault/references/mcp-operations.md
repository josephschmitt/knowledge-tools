# MCP Operations (claude.ai-facing)

The vault's MCP server runs on homelab and is connected to claude.ai as a custom
connector. It exposes the five tools below. These shapes mirror the server
(`mcp/src/mcp.ts`); if the server changes, update this file and `SKILL.md` to match.

All tools return their result as plain text content. Paths are relative to `wiki/`.

## Operations

### append_to_inbox
- **Purpose:** append a raw capture to `inbox/`. Dumb — no processing.
- **Inputs:** `text` (the raw capture content, required); `title` (optional short
  title). There is no separate `source_url` or `note` field — fold a source link and a
  one-line of context into `text`.
- **Output:** a confirmation naming the inbox path written, e.g. `Captured to
  inbox/2026-06-07T…-<slug>.md. It will be compiled into the wiki on the next nightly
  run.`
- **Notes:** never touches `wiki/` — the vault is mounted read-only except `inbox/`.
  One call writes one new file under `inbox/`.

### search_wiki
- **Purpose:** find notes relevant to a query. This is the primary read path.
- **Inputs:** `query` (text to search for, required). No `limit`.
- **Output:** text. For each matching note, a `## <note-path>` header followed by
  blockquoted snippets (the matching line plus a little surrounding context). When
  nothing matches it says so.
- **Notes:** case-insensitive substring search across the body of every wiki note. Use
  the `<note-path>` from a hit with `get_note` to read the full note.

### get_note
- **Purpose:** read a specific note's full content.
- **Inputs:** `path` (a note path or name relative to `wiki/`, with or without the
  `.md` extension — e.g. `homelab-infrastructure`).
- **Output:** the note's full markdown, or a "Note not found" message.

### list_index
- **Purpose:** read `index.md`, the wiki's navigation map.
- **Inputs:** none.
- **Output:** `index.md` markdown.
- **Notes:** the orientation entry point. `index.md` lives at the vault root, so it's a
  separate tool rather than a `get_note` path.

### list_notes
- **Purpose:** enumerate every wiki note.
- **Inputs:** none.
- **Output:** a newline-separated list of note paths (relative to `wiki/`).
- **Notes:** useful when a search comes up empty or you need to see what exists before
  searching.

## Out of scope for this surface

- **Compiling.** There is no compile tool. The inbox is compiled into the wiki by a
  scheduled nightly job on homelab; this interface cannot trigger it.
- **Wiki maintenance and health checks** (broken links, deduping, gap-finding) run on
  homelab.
- **Anything that reorganizes or rewrites the wiki** from this interface — the vault is
  read-only here except for inbox captures.
