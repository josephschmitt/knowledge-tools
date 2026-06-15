# MCP Operations (claude.ai-facing)

The vault's service runs on homelab and is connected to claude.ai as a custom MCP
connector. It exposes the ten tools below. These shapes mirror the server
(`service/src/mcp.ts`); if the server changes, update this file and `SKILL.md` to match.

All tools return their result as plain text content. Paths are relative to `wiki/`.

> The same service also exposes these operations as a **REST API** under `/api/v1` (for
> scripts/tooling that don't speak MCP), returning JSON instead of text — e.g.
> `search_wiki` → `GET /api/v1/wiki/search?q=`, `append_to_inbox` → `POST /api/v1/inbox`,
> `answer_question` → `POST /api/v1/questions/<id>/answer`. The REST routes call the same
> in-process core, so behavior matches the tools 1:1. Full route table in
> `service/README.md`; keep both in sync with the server when shapes change.

## Operations

### append_to_inbox
- **Purpose:** append a raw capture to `inbox/`. Dumb — no processing.
- **Inputs:** `text` (the raw capture content, required); `title` (optional short
  title). There is no separate `source_url` or `note` field — fold a source link and a
  one-line of context into `text`.
- **Output:** a confirmation naming the inbox path written, e.g. `Captured to
  inbox/2026-06-07T…-<slug>.md. It will be compiled into the wiki on the next scheduled
  compile.`
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
  scheduled job. Only needed to process the inbox sooner; capturing alone never
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
  scheduled job (one compile at a time) are enforced server-side; the scheduled run is
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

### list_questions
- **Purpose:** list the judgment calls the vault's maintenance pass has raised for Joe —
  contradictions it found between notes, or claims it can't verify internally.
- **Inputs:** `status` (optional; one of `open`, `answered`, `applied`). Omit for all.
  `open` = awaiting Joe's answer; `answered` = decided but not yet applied to the wiki;
  `applied` = done.
- **Output:** text — one line per question: `[status] <id> (<kind>) — <one-line summary>`,
  where `kind` is `judgment-call` or `needs-verification`. Says so when none match.
- **Notes:** use an `id` from a hit with `get_question` or `answer_question`. The server is
  configured for one of two backends — the `inbox/.review/` file queue, or the vault's GitHub
  issues (then `<id>` is the issue number) — and these tools work the same against either.

### get_question
- **Purpose:** read one judgment call's full context before answering it.
- **Inputs:** `id` (from `list_questions`).
- **Output:** the question's full markdown — the contradiction or claim, the notes
  involved, and any prior discussion — or a "Question not found" message.

### answer_question
- **Purpose:** record Joe's decision on a judgment call. This is the only write this
  surface makes beyond inbox captures.
- **Inputs:** `id` (from `list_questions`) and `answer` (Joe's decision, in his words).
- **Output:** a confirmation that the answer was recorded and marked answered.
- **Notes:** marks the call `answered`; the next maintenance pass on homelab applies it to
  the wiki and closes it (or follows up with a sharper question if the answer is ambiguous,
  which reappears as an open question). Does **not** edit the wiki from this interface. On the
  file backend it writes the answer under `inbox/.review/`; on the GitHub backend it comments
  the answer and adds the `vault:answered` label, exactly as answering on github.com would.

## Out of scope for this surface

- **Synthesis.** `compile_run` only *triggers* a compile; the actual inbox→wiki
  synthesis runs on homelab under `CLAUDE.md`, never in this interface.
- **Wiki maintenance and health checks** (broken links, deduping, gap-finding) run on
  homelab.
- **Anything that reorganizes or rewrites the wiki** from this interface — the vault is
  read-only here except for inbox captures (and the compile request sentinel).
