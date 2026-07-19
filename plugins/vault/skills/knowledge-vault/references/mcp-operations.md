# MCP Operations (I/O shapes)

The vault's service runs on the vault host and is connected as a custom MCP connector.
This file is the **I/O-shape mirror** of `service/src/mcp.ts` — inputs, outputs, and return
formats only. The *rules* for each tool live in the tool's own description (loaded with the
tool); the *why* and choreography live in `SKILL.md`. Keep this in sync with the server when
shapes change; don't restate rules or rationale here.

All tools return their result as plain text content. Note paths are area-qualified
(`library/<rel>` or `notebook/<rel>`); an unqualified path is treated as `library/`. `tasks/` is
not on the query surface.

> The same service also exposes these operations as a **REST API** under `/api/v1` (for
> scripts/tooling that don't speak MCP), returning JSON instead of text — e.g. `search_notes`
> → `GET /api/v1/search?q=`, `append_to_inbox` → `POST /api/v1/inbox`, `answer_question`
> → `POST /api/v1/questions/<id>/answer`. Same in-process core, so behavior matches 1:1. Full
> route table in `service/README.md`.

> **Agent-driven deployments** (the local stdio server, or HTTP with `KNOWLEDGE_AGENT_DRIVEN`
> set): the three job triggers below change shape — instead of triggering a host job,
> `compile_run` / `synthesize_run` / `resolve_run` return the body of the vault's own
> `.agents/skills/<job>/SKILL.md` procedure for the *calling* agent to run (`compile_run`
> still returns the empty-inbox message when there's nothing to compile, and `model`/`effort`
> are ignored). `vault_status` keeps its schema but `last_compiled_at` and the `jobs` timing
> fields stay `null` — `pending_inbox_count` is the meaningful field.

## Shapes

### append_to_inbox
- **Inputs:** `text` (required; raw capture content). `title` (optional). No separate
  `source_url`/`note` field.
- **Output:** confirmation naming the inbox path, e.g. `Captured to
  inbox/2026-06-07T…-<slug>.md. It will be compiled into the library on the next scheduled compile.`

### search_notes
- **Inputs:** `query` (required). `scope` (optional; one of `library`, `notebook`, `both`;
  default `library`). No `limit`.
- **Output:** for each matching note, a `## <area>/<note-path>` header (notebook hits suffixed
  ` (tentative)`) followed by blockquoted snippets (matching line + a little context). Says so
  when nothing matches. Never searches `tasks/`.

### get_note
- **Inputs:** `path` (note path or name, optionally area-qualified `library/…` / `notebook/…`,
  with or without `.md`; unqualified resolves under `library/`).
- **Output:** the note's full markdown, or a "Note not found" message.

### list_index
- **Inputs:** none.
- **Output:** the library `index.md` (its navigation map; includes the `## Tasks` block) and the
  notebook index, each under a labeled header. A missing index is noted in place.

### list_notes
- **Inputs:** none.
- **Output:** newline-separated list of area-qualified note paths (`library/<rel>` and
  `notebook/<rel>`).

### compile_run
- **Inputs:** `model` (optional string), `effort` (optional string) — override this run's model /
  reasoning effort; empty falls back to the host's config/env chain then the harness default.
- **Output:** text describing one of four outcomes — *triggered*, *throttled* (names when the
  next manual compile is available), *busy*, or *empty*. Asynchronous: returns immediately
  without a result summary. Agent-driven mode instead returns the compile procedure text for
  the caller to run (or the *empty* message).

### synthesize_run
- **Inputs:** `model` (optional string), `effort` (optional string) — as in `compile_run`.
- **Output:** text confirming the whole-corpus synthesize pass was triggered. Asynchronous:
  returns immediately without a result summary. Poll `vault_status` → `jobs.synthesize`:
  `running` is `true` while it runs and flips `false` when it finishes; `summary` describes the
  outcome. Agent-driven mode instead returns the synthesize procedure text for the caller to run.

### resolve_run
- **Inputs:** `model` (optional string), `effort` (optional string) — as in `compile_run`.
- **Output:** text confirming the resolve pass (applies answered judgment calls) was triggered.
  Asynchronous: returns immediately; a no-op host-side when nothing is answered. Poll
  `vault_status` → `jobs.resolve` (`running` flips `false` when done; `summary` notes the
  outcome, e.g. `nothing to resolve`). Agent-driven mode instead returns the resolve procedure
  text for the caller to run.

### vault_status
- **Inputs:** none.
- **Output:** a JSON object with six fields:
  - `vault_name` — this vault's label (`KNOWLEDGE_VAULT_NAME`), or `null` when unlabeled. Only
    meaningful with several vaults connected; it disambiguates which vault answered.
  - `last_compiled_at` — ISO time the most recent *successful* compile finished, or `null`.
  - `pending_inbox_count` — number of captures in `inbox/` not yet compiled.
  - `manual_compile_available_at` — ISO time the next manual `compile_run` is allowed; `null`
    or a past time means now.
  - `running` — `true` while a compile is in progress.
  - `jobs` — per host job, as `{ "compile": {...}, "synthesize": {...}, "resolve": {...} }`, where
    each value is `{ "last_run_at": <iso|null>, "next_run_at": <iso|null>, "running": <bool>,
    "started_at": <iso|null>, "summary": <string|null> }`. The timing fields are the last/next
    *scheduled* run (`null` when unknown — job not yet run); `running`/`started_at`/`summary` are
    the live run status (`running` flips `false` when the job finishes; `false`/`null` when the
    host predates the per-job status surface). A job's `next_run_at` is its scheduled cadence —
    distinct from `manual_compile_available_at` (the on-demand compile cooldown).

  Agent-driven mode keeps this schema, but `last_compiled_at` and the `jobs` timing fields stay
  `null` — `pending_inbox_count` is the meaningful field.

### list_questions
- **Inputs:** `status` (optional; one of `open`, `answered`, `applied`). Omit for all.
- **Output:** text — one line per question: `[status] <id> (<kind>) — <one-line summary>`,
  where `kind` is `judgment-call` or `needs-verification`. Says so when none match. (On the
  GitHub backend, `<id>` is the issue number.)

### get_question
- **Inputs:** `id` (from `list_questions`).
- **Output:** the question's full markdown, or a "Question not found" message.

### answer_question
- **Inputs:** `id` (from `list_questions`) and `answer` (the user's decision, in their own words).
- **Output:** a confirmation that the answer was recorded and marked answered.
