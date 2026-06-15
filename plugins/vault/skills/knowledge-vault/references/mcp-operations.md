# MCP Operations (I/O shapes)

The vault's service runs on homelab and is connected to claude.ai as a custom MCP connector.
This file is the **I/O-shape mirror** of `service/src/mcp.ts` — inputs, outputs, and return
formats only. The *rules* for each tool live in the tool's own description (loaded with the
tool); the *why* and choreography live in `SKILL.md`. Keep this in sync with the server when
shapes change; don't restate rules or rationale here.

All tools return their result as plain text content. Wiki paths are relative to `wiki/`.

> The same service also exposes these operations as a **REST API** under `/api/v1` (for
> scripts/tooling that don't speak MCP), returning JSON instead of text — e.g. `search_wiki`
> → `GET /api/v1/wiki/search?q=`, `append_to_inbox` → `POST /api/v1/inbox`, `answer_question`
> → `POST /api/v1/questions/<id>/answer`. Same in-process core, so behavior matches 1:1. Full
> route table in `service/README.md`.

## Shapes

### append_to_inbox
- **Inputs:** `text` (required; raw capture content). `title` (optional). No separate
  `source_url`/`note` field.
- **Output:** confirmation naming the inbox path, e.g. `Captured to
  inbox/2026-06-07T…-<slug>.md. It will be compiled into the wiki on the next scheduled compile.`

### search_wiki
- **Inputs:** `query` (required). No `limit`.
- **Output:** for each matching note, a `## <note-path>` header followed by blockquoted
  snippets (matching line + a little context). Says so when nothing matches. Searches `wiki/`
  only — not `tasks/`.

### get_note
- **Inputs:** `path` (note path or name relative to `wiki/`, with or without `.md`).
- **Output:** the note's full markdown, or a "Note not found" message.

### list_index
- **Inputs:** none.
- **Output:** `index.md` markdown (the wiki's navigation map; includes the `## Tasks` block).

### list_notes
- **Inputs:** none.
- **Output:** newline-separated list of note paths (relative to `wiki/`).

### compile_run
- **Inputs:** none.
- **Output:** text describing one of four outcomes — *triggered*, *throttled* (names when the
  next manual compile is available), *busy*, or *empty*. Asynchronous: returns immediately
  without a result summary.

### vault_status
- **Inputs:** none.
- **Output:** a JSON object with four fields:
  - `last_compiled_at` — ISO time the most recent *successful* compile finished, or `null`.
  - `pending_inbox_count` — number of captures in `inbox/` not yet compiled.
  - `manual_compile_available_at` — ISO time the next manual `compile_run` is allowed; `null`
    or a past time means now.
  - `running` — `true` while a compile is in progress.

### list_questions
- **Inputs:** `status` (optional; one of `open`, `answered`, `applied`). Omit for all.
- **Output:** text — one line per question: `[status] <id> (<kind>) — <one-line summary>`,
  where `kind` is `judgment-call` or `needs-verification`. Says so when none match. (On the
  GitHub backend, `<id>` is the issue number.)

### get_question
- **Inputs:** `id` (from `list_questions`).
- **Output:** the question's full markdown, or a "Question not found" message.

### answer_question
- **Inputs:** `id` (from `list_questions`) and `answer` (Joe's decision, in his words).
- **Output:** a confirmation that the answer was recorded and marked answered.
