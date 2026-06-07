---
description: Compile every item in inbox/ into the wiki (used by the nightly job)
model: opus
---

Process **every** item currently in `inbox/` (top-level files only; ignore
`inbox/archive/`) following the **Compiling the inbox** section of `CLAUDE.md`:

1. For each item, decide its shape (decision / reference / concept-hub).
2. Search `wiki/` first for related notes — prefer updating or linking an existing note
   over creating a near-duplicate.
3. Write the entry as durable knowledge (present tense, stated as established fact;
   preserve the *why* and tradeoffs; no transcript voice).
4. Add `[[wikilinks]]` to related notes, and link back from them where it helps.
5. Update `index.md` to reflect anything added or substantially changed.
6. Append a one-line, ISO-dated entry to `log.md` (newest at the bottom) noting what you
   compiled this run.

Then stop. **Do not** delete or move anything in `inbox/`, and **do not** run git — the
nightly wrapper archives the processed inbox files and commits the result (including your
`log.md` and `index.md` edits). Your job is only to turn `inbox/` captures into `wiki/`
knowledge, keep `index.md` current, and record the run in `log.md`.

If `inbox/` has no top-level files to process, say so and make no changes.
