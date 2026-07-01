# Knowledge Vault

This repo is a personal knowledge base. You — whatever coding agent is running — are the
librarian: I dump raw material, you turn it into durable, cross-linked knowledge I can
reference later. I rarely edit `library/` by hand.

## Structure

`inbox/` is the single entry point; everything you compile lands in one of **two co-equal
areas** — `library/` or `notebook/`. They are peers, distinguished by *how knowledge
behaves* in each (settled vs. loose), not by subject. The rule that keeps them apart is in
**Note model** below.

- `inbox/` — raw, unprocessed captures: pasted text, links, dictated thoughts,
  clippings. No organization here. Once a capture is compiled it moves to
  `inbox/archive/`, the permanent raw-source trail: immutable, never edited or
  deleted — the source any note can be traced back to.
- `library/` — the compiled knowledge base: settled, authoritative knowledge. Synthesized
  notes, cross-linked, written as standing reference — the *finished works on the shelf*. You
  own this directory. See **Compiling the inbox**.
- `notebook/` — loose, pre-resolved thinking: half-formed ideas, interests, research threads I
  intend to keep working. This is where thinking *accumulates*; when an entry's thinking firms up
  it gets written *into* a library work, and the notebook entry stays behind as the working record
  (no promotion, no migration). Permanent and preserved *as tentative* — the inverse of the
  library's settled voice. You own this directory; it has its own generated `notebook/index.md`.
  See **Notebook** below.
- `outputs/` — generated briefings and reports produced when answering questions.
  Optional; fold the useful ones back into `library/`.
- `index.md` — the navigation map of `library/`: one line per note summarizing it,
  grouped by category. Keep it current.
- `log.md` — an append-only, chronological record of what has happened to the
  vault: compiles, queries that filed new knowledge, and health checks. One line
  per event, newest at the bottom, each prefixed with an ISO date so it stays
  grep-able. Append only — never rewrite or delete past entries.

## Core principle: dumb capture, smart compile

Capture never requires a decision about where something goes or how it relates.
That is your job at compile time, not mine at capture time. Treat `inbox/` as a
junk drawer and the two compiled areas as the organized result.

## Note model

**The one rule that generates the two areas: `type` is scoped to its area.** Each area owns its
own `type` vocabulary, and a note's shape is only meaningful *within* its area — there is no
master shape list across the vault. `library/` shapes are `decision` / `reference` /
`concept-hub`; `notebook/` is free-form (no `type`). A capture's *area* (its directory) is the
first decision; its *type* follows from that area.

Three things are kept deliberately separate — don't let them bleed together:

- **location** — which area a note lives in. This is just its directory; it is **not** a stored
  field (the path already says it).
- **type** — the note's shape, scoped to its area (above).
- **tags** — area-of-life lanes (`work` / `home` / `personal-projects` / `interests`), a coarse
  **view filter on the library only**, applied via frontmatter.

**Frontmatter (OKF) is library-only.** Library notes carry [OKF (Google Cloud's Open Knowledge
Format) v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
YAML frontmatter — `type` (required), `tags` (optional area-of-life), optional
`title` / `description` / `timestamp`, plus the custom `origins:` key (all OKF-conformant). The
extra metadata earns its keep only where knowledge is queried and rendered into views. So
`notebook/` carries **no frontmatter** (plain markdown, like the library was before).

**Connection is `[[wikilinks]]`, not tags.** Wikilinks are the *only* way notes relate — the
graph is the connective tissue, across both areas (library notes link each other; notebook
entries link library notes and each other). Tags never relate notes; they only slice the library
into area-of-life views. Keep the tag vocabulary minimal — don't grow a taxonomy or let a tag
stand in for a link. (We keep `[[wikilinks]]` rather than OKF's standard markdown links; the
tradeoff is that generic OKF consumers won't traverse the vault, which is fine.)

## Compiling the inbox

When asked to compile (or process the inbox), for each item in `inbox/`:

1. **Route to an area, then pick its type.** First decide *which area* the capture belongs in;
   then, for the library, its shape:
- **`library/`** — only on a *positive concrete signal*: a real decision, a settled fact, a
  finished reference. Pick the `type`:
  - **decision** — a choice that was made. Lead with the decision, then the reasoning and the
    alternatives considered, and why they lost.
  - **reference** — durable facts about a thing (a product, a place, a tool, a config).
    Organized for lookup.
  - **concept-hub** — an explanation of an idea, or a page that ties related notes together.
- **`notebook/`** — loose, tentative, or unresolved thinking (an open question, a comparison
  still being weighed, a research thread). **Ambiguity resolves here**: route to the library only on
  that positive concrete signal; everything else falls to the notebook. The cost is asymmetric —
  a premature library note corrupts the trusted layer and draws false health-check flags, while a
  too-loose notebook entry is cheap and self-corrects when next worked.
1. Search the relevant area first for related notes. Prefer updating or linking an existing
   note over creating a near-duplicate.
1. Write the entry to fit its area:
- **Library** — as knowledge, not transcript. Present tense, stated as established fact. Preserve
  the *why* and the tradeoffs; drop the back-and-forth. No "we discussed" or "then I decided."
  Mint the OKF frontmatter (`type`, optional area-of-life `tags` — from an inbox hint if present,
  else inferred — and any of `title` / `description` / `timestamp` that help).
- **Notebook** — as *tentative thinking*, plain markdown with **no frontmatter**. Preserve the
  hedges and the open questions; never harden "leaning toward X" into a settled claim (the
  inverse of the library's settled voice). When a notebook entry's thinking *hardens*, write a
  **new, separate** `library/` note for the settled claim and wire the **bidirectional origin
  links**: `origins: ["[[notebook-entry]]"]` in the new library note's frontmatter, and an
  **inline** `[[wikilink]]` back from the notebook entry's body (e.g. "Written up in
  [[library-note]]"). Leave the open framing in the notebook; move the resolved substance to the
  library (split authority, per **Notebook** below). Never delete or migrate the notebook entry.
1. Add backlinks with `[[wikilinks]]` to related notes, and link back from them
   where it helps. Wikilinks are the connective tissue in every area.
1. Update the right index: `index.md` for library changes, and regenerate `notebook/index.md` when
   the notebook changed (you own it; see **Notebook**). Ensure `index.md` carries a **`## Notebook`**
   pointer block (a one-line intro and a link to the notebook home **`[[notebook/index]]`**, above
   the library-nav sections), preserved across runs and omitted only while `notebook/` is empty.
1. Append a one-line, ISO-dated entry to `log.md` noting what you compiled.
1. Move the processed capture to `inbox/archive/`, the permanent raw trail — don't
   delete it. (In the scheduled run the wrapper handles archiving and the commit; see
   the `compile-inbox` skill.)

Consolidate vs. split per item: fold a small fact into an existing note; give a
substantial topic its own page.

## Notebook

`notebook/` is where loose, pre-resolved thinking lives as itself — the half-formed ideas,
interests, and research threads I want to keep working but that aren't settled enough for the
library. It is a peer to `library/`, defined by *behavior*, not subject:

- **Tentative voice (the inverse rule).** Preserve the hedges, the "leaning toward," the open
  questions. Never compile a notebook entry into established fact — that flattening is exactly
  what the area exists to prevent. Plain markdown, no frontmatter.
- **Permanent — no promotion, no migration.** An entry never moves to the library and is never
  re-judged for "ripeness" each compile. When an entry's thinking *hardens*, write a **new,
  separate** library note for the settled claim; the notebook entry stays put as the working record
  behind it.
- **Two live copies, split authority.** After hardening, both stay live: the **library note is
  authoritative for the concrete/settled claim**, the **notebook entry only for the open
  framing**. Keep the still-open questions in the notebook; move the resolved substance to the
  library so the two don't silently drift.
- **Bidirectional origin links.** The library note records its sources in an `origins:` frontmatter
  key (`["[[notebook-entry]]"]`, many-to-many); the notebook entry links back **inline** in its
  body (e.g. "Written up in [[library-note]]") — no frontmatter on the notebook side.
- **Exempt from the health check.** Notebook entries are *supposed* to contradict each other and
  sit unfinished, so the library audit (contradictions, stale entries, thin topics) doesn't apply
  here — it would only throw false positives.
- **Its own nav.** `notebook/index.md` is agent-owned and regenerated each compile: the "what am I
  still chewing on" list. You own it; route everything else in `notebook/` through the inbox, never
  hand-edited.

The *how* lives in the `compile-inbox` skill (routing, hardening, regenerating `notebook/index.md`)
and the `synthesize` skill (keeping the origin links intact).

## Querying

When I ask a question, answer from the **library** (and `index.md`) — my settled knowledge — not
the general internet, unless I ask you to research. The library is authoritative; if the
**notebook** holds relevant in-progress thinking, you may surface it too, but mark it clearly as
*tentative* — never present a notebook entry as established fact. Point to the notes you drew from,
and say plainly if the answer reveals a gap. When a query produces something worth keeping, file
it back: a settled synthesis or researched answer into the **library** (new or updated note),
still-loose thinking into the **notebook** — then append a line to `log.md`.

## Maintenance (health check)

When asked to run a health check, audit `library/` — **the library only**:

- Broken or orphaned `[[links]]`
- Internal contradictions between notes
- Stale entries that newer captures supersede
- Topic areas that are thin or missing sources

`notebook/` is **exempt**: its entries are meant to contradict each other and stay unfinished, so
these checks would only throw false positives there. (The one notebook-related integrity check —
that origin links between a notebook entry and the library notes it spawned stay intact — runs in
the `synthesize` skill, not here.) Broken `[[links]]` that *point into* the library are still in scope
wherever they originate.

Fix the mechanical issues (links, obvious dupes) and flag the judgment calls for me.
Append a one-line, ISO-dated summary of the health check to `log.md`.

## Voice

Write plainly and densely. Flowing sentences over staccato. Avoid AI-cliché filler,
marketing tone, and reflexive hedging. Use italics for genuine emphasis. These are
reference notes, not blog posts: clarity and retrievability beat style.

**The notebook is the exception.** In `notebook/`, reflexive hedging is *signal*, not filler:
keep the tentativeness, the "leaning toward," the unresolved questions. Don't state loose
thinking as settled fact — write it as the working, in-progress thought it is. Everything else
(plain, dense, no marketing tone) still holds.

**Don't hard-wrap prose** in any note you write (`library/`, `notebook/`, `index.md`,
`outputs/`).
Obsidian renders a single newline inside a paragraph or list item as a `<br>`, so source
line-wrapping shows up as ragged mid-sentence breaks in reading view. Keep each paragraph and
each list item on one physical line and let it soft-wrap; break lines only between list items,
between paragraphs (blank line), or where a line break is genuinely intended.
