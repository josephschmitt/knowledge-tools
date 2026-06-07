# MCP Operations (claude.ai-facing)

The vault's MCP server runs on homelab and is connected to claude.ai as a custom
connector. It exposes the seven tools below. These shapes mirror the server
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

### compile_run
- **Purpose:** trigger an on-demand compile of the inbox into the wiki, on top of the
  scheduled nightly job. Only needed to process the inbox sooner; capturing alone never
  requires it.
- **Inputs:** none.
- **Output:** text describing one of four outcomes —
  - *triggered* — the compile has started; the wiki updates once it finishes.
  - *throttled* — refused: a manual compile ran within the cooldown window (one per
    hour); names when the next is available. Don't retry.
  - *busy* — a compile is already running. Don't trigger another.
  - *empty* — the inbox has nothing to compile (no cooldown consumed).
- **Notes:** asynchronous — the tool kicks off the compile and returns immediately; it
  does not wait for or return a summary of the result. Synthesis runs on homelab under
  `CLAUDE.md`, not in this interface. The manual cooldown and a lock shared with the
  nightly job (one compile at a time) are enforced server-side; the scheduled run is
  never throttled and does not consume the manual cooldown.

### vault_status
- **Purpose:** check, at a glance, whether the vault is caught up — when the last compile
  finished, how many captures are still waiting, and when a manual compile is next allowed.
  This is the completion signal `compile_run` lacks (it returns before the compile finishes).
- **Inputs:** none.
- **Output:** a JSON object with four fields:
  - `last_compiled_at` — ISO time the most recent *successful* compile finished, or `null`
    if none yet. A value newer than your `compile_run` trigger time means that run
    completed and the wiki is caught up.
  - `pending_inbox_count` — number of captures in `inbox/` not yet compiled.
  - `manual_compile_available_at` — ISO time the next manual `compile_run` is allowed.
    `null` or a past time means available now; a future time means the hourly cooldown is
    still active.
  - `running` — `true` while a compile is in progress.
- **Notes:** cheap to call repeatedly. After triggering `compile_run`, poll this until
  `last_compiled_at` advances past your trigger time (or `running` goes false) to know the
  wiki is up to date.

## Out of scope for this surface

- **Synthesis.** `compile_run` only *triggers* a compile; the actual inbox→wiki
  synthesis runs on homelab under `CLAUDE.md`, never in this interface.
- **Wiki maintenance and health checks** (broken links, deduping, gap-finding) run on
  homelab.
- **Anything that reorganizes or rewrites the wiki** from this interface — the vault is
  read-only here except for inbox captures (and the compile request sentinel).
