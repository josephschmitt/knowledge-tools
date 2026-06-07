---
name: knowledge-vault
description: Capture raw material into Joe's personal knowledge vault and answer questions from it, through the vault's MCP connector. Use this whenever Joe wants to SAVE something for later — "save this to my knowledge base", "add this to my second brain", "capture this", "remember this", "file this away", "dump this in the vault" — or wants to RECALL what he already knows — "what do I know about X", "do I have notes on Y", "check my vault", "look this up in my notes", "what did we figure out about Z". Use it even when Joe doesn't name the vault explicitly but is clearly trying to stash a finding or pull up prior knowledge. Capture writes raw, unorganized material to the inbox and does NOT synthesize or categorize it; querying searches and reads the compiled wiki and answers from Joe's own notes. Heavy compilation and wiki maintenance run automatically on homelab and are out of scope here — do not attempt them from this interface.
---

# Knowledge Vault

This skill is the conversational front door to Joe's personal knowledge base: the
`knowledge` repo, a markdown vault living on his home server (homelab), reachable
from here through its MCP connector. Two jobs live in this interface — capturing raw
material and answering questions from what's already been compiled.

## How the system is split

The work is divided on purpose, so don't duplicate it:

- **This interface (claude.ai): capture + query.** Drop raw material into the inbox,
  and answer Joe's questions from the compiled wiki.
- **homelab (Claude Code + `CLAUDE.md` + a scheduled job): compile + maintain.** The
  heavy synthesis — turning the inbox into durable, cross-linked notes, and keeping the
  wiki healthy — runs there, where there's full filesystem access and the vault's
  conventions live.

The reason capture stays dumb here is that synthesis is better done in one place, on a
schedule, with the whole vault in view. Pre-organizing a capture from this interface
just fights that.

## MCP operations

The exact tools and their input/output shapes live in `references/mcp-operations.md`.
Read it before calling anything. At a high level the connector exposes six tools:
`append_to_inbox` to capture, `search_wiki` to find notes, `get_note` to read one,
`list_index` to read the navigation map, `list_notes` to enumerate every note, and
`compile_run` to trigger an on-demand compile (rate-limited; see below).

## Capturing

When Joe wants to save something, append it to the inbox raw with `append_to_inbox`
and stop there.

- Capture the *content*, not the conversation. When Joe says "save this," work out what
  "this" is — the conclusion, the snippet, the link — and capture that, not the whole
  transcript.
- `append_to_inbox` takes the capture `text` plus an optional short `title`. There are
  no separate fields for a source URL or context, so fold those into the `text`: include
  the source URL if there is one, plus a single line of what it is or why it matters.
  That gives the compiler something to work with. Nothing more.
- Do not categorize, synthesize, pick a destination, or write a polished note. The
  compiler on homelab does that. Pre-organizing here defeats the inbox.
- Confirm briefly what went in. The tool returns the inbox path it wrote; relay that the
  capture will fold into the wiki on the next nightly compile.

**Example:**
Joe: "Save this to my knowledge base — we landed on the Waterfield Legion Go 2 case for the GPD Win 5."
You: call `append_to_inbox` with the decision (and any link) as `text`, then →
"Captured to the inbox: the GPD Win 5 case decision (Waterfield Legion Go 2). It'll fold into the wiki on the next compile."

## Querying

When Joe asks what he knows, or to look something up, answer from the vault.

- Search the wiki first with `search_wiki`, then read the relevant notes with
  `get_note`. `search_wiki` returns matching note paths, each with short snippets around
  the match — use the path with `get_note` to pull the full note. Use `list_index` to
  orient if it helps, and `list_notes` to see everything when a search comes up empty or
  you're not sure what exists.
- Answer from his own notes, not general web knowledge, when he's asking what *he*
  knows. Point to the notes you drew on by path.
- If the wiki has nothing, say so plainly — don't invent a note or a citation. Offer to
  capture the current info, or to research it on the web if that's what he wants.
- If notes conflict or look stale, surface that instead of silently picking one.

**Example:**
Joe: "What do I know about lake house options for the trip with Adam?"
You: `search_wiki` → `get_note` on the matching paths → answer from them, naming the
note(s); if it's thin, say where the gap is.

## Triggering a compile

A scheduled job on homelab compiles the inbox into the wiki nightly, so a manual compile
is occasional, not routine — capturing alone never requires it. When Joe explicitly wants
the inbox processed sooner, call `compile_run` and act on what it returns. The compile
runs asynchronously on homelab: the tool only *kicks it off* and returns right away; the
wiki updates once the run finishes, and captures stay safe in the inbox until then. Never
run the synthesis yourself from this interface — it belongs on homelab where the full
vault and the `CLAUDE.md` conventions live.

- **Triggered:** the compile has started. Tell Joe it's running and the wiki will update
  shortly; his captures are safe meanwhile.
- **Throttled (refused):** a manual compile ran within the last hour and the cooldown is
  still active, so the call is refused. Relay when the next manual compile is available,
  and reassure him his captures are safe — the nightly job will process them regardless.
  **Don't** retry.
- **Busy:** a compile is already running. Let him know; **don't** trigger another.
- **Empty:** the inbox has nothing to compile. Say so; nothing to do.

## Conventions

Stay aligned with the vault's `CLAUDE.md` so this interface and homelab agree. The one
that matters at this layer: capture raw, never pre-organize. When the compiler writes
notes it follows `CLAUDE.md` (knowledge not transcript, present tense, search-first
deduping, `[[wikilinks]]`); you don't need to reproduce any of that at capture time.
