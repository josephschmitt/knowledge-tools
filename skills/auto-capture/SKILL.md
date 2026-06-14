---
name: auto-capture
description: Proactively and autonomously capture knowledge to Joe's vault inbox during ANY conversation — without being asked. Engage this skill on your own initiative whenever the discussion surfaces something worth keeping — a durable fact, decision, preference, finding, link, snippet, or conclusion (capture it as knowledge), or a concrete thing Joe needs to do, follow up on, buy, fix, or decide (capture it as a task). This is the always-on counterpart to the knowledge-vault skill, which fires when Joe says "save this"; THIS one fires when he didn't ask but the material is clearly capture-worthy. Joe's standing rule is to over-capture rather than under-capture, so nothing is lost because he forgot to ask. Capture is raw and takes zero decisions — it routes through the vault's append_to_inbox, and the homelab compiler sorts, dedupes, and files everything later. Skip only the trivial or transient; ask before capturing only in the rare case something looks private or sensitive enough that he might not want it persisted.
---

# Auto-Capture

This skill makes you capture knowledge to Joe's vault **on your own initiative**. The
companion `knowledge-vault` skill handles the explicit path — when Joe says "save this" —
and all querying. This one handles everything Joe *doesn't* say: the findings, decisions,
and to-dos that surface mid-conversation and would otherwise be lost the moment the chat
scrolls away.

## Why autonomous over-capture

The failure mode this skill exists to kill is the **silent miss**: a useful fact or a real
action item goes by, Joe doesn't think to ask you to save it, and it's gone. Joe's standing
instruction is explicit: **over-capture rather than under-capture.** A redundant capture
costs nothing — the compiler folds it into an existing note or drops it. An uncaptured
thought is a total loss. So when you're genuinely on the fence about whether something is
worth keeping, the answer is capture it.

You do not need permission for each capture. Capturing is the default behavior, not a thing
you stop to ask about.

## What's worth capturing

Capture proactively, as a rule of thumb, whenever the conversation produces:

- **Durable knowledge** — a fact Joe will want later, a decision he landed on, a preference
  or constraint he stated, a conclusion you reached together, a useful link or source, a
  snippet / quote / config worth keeping, a non-obvious "how X works" he just worked out.
- **A task** — something Joe needs to *do*: a follow-up, a thing to buy, a bug to fix, a
  person to contact, a decision to revisit — anything with a verb pointed at Joe.

Both go through the **same** tool; there's no separate task-vs-knowledge field. The only
difference is how you word the `text`: for a task, lead with `TODO:` (or `Task:`) and make
the action — and any deadline — explicit, so the compiler files it as actionable rather
than reference.

Lean toward capturing. Skip only the genuinely throwaway: small talk and passing chatter, a
fact already obviously and durably Joe's, or something purely transient to the current turn
(a one-off calculation he won't need again).

## How to capture

Drop it into the inbox raw with `append_to_inbox` and stop there.

**Capture takes zero decisions — just dump.** Do not search the wiki for duplicates, do not
judge whether it's "really" worth it past the lean-toward-capture bar above, and do not
categorize, synthesize, pick a destination, or write a polished note. The homelab compiler
does all of that later, with the whole vault in view ("dumb capture, smart compile").
Pre-organizing here just fights it.

- **Capture the *content*, not the transcript.** Work out the actual durable thing — the
  decision, the link, the conclusion, the action — and capture that, not the back-and-forth
  that produced it.
- **Make the dump legible.** Fold the source URL (if there is one) and a single line of what
  it is or why it matters into the `text` — there are no separate fields for those.
- **One capture per distinct item.** If a turn surfaced two unrelated things, that's two
  `append_to_inbox` calls.

## Announcing captures

Capturing is autonomous, but it isn't *secret*. Tell Joe what you filed — briefly and
unobtrusively — so he can catch a mis-capture, but never let it derail the conversation
you're actually having.

- A short trailing note is enough: "(Captured that to your vault: the GPD Win 5 case
  decision.)" Keep it to a line. Don't pause the real work to announce it, and don't ask for
  approval after the fact.
- If you filed several things across a longer exchange, a single batched line at a natural
  break beats interrupting repeatedly.
- If Joe says a capture was unwanted, just don't re-file it — there's nothing to undo here;
  noise gets dropped at compile time.

## The rare ask-first

The default is capture-without-asking. Ask *before* capturing only when you genuinely can't
tell whether Joe would want it **persisted at all** — almost always a privacy or sensitivity
call: something personal about another person, a credential or secret, or clearly sensitive
personal information that living in the vault might be wrong for. That's the exception, and
it should be rare. Worth-it-or-not is *not* a reason to ask — for that, just capture.

## Altitude — stay out of the compiler's lane

This skill only *captures*. It never compiles, dedupes, files, or queries — turning the
inbox into durable, cross-linked notes (and deciding where a task or note really belongs)
happens on homelab on a schedule, under the vault's own `CLAUDE.md`. Querying the wiki and
answering judgment calls live in the `knowledge-vault` skill. Don't reproduce any of that
here. Capture raw, announce briefly, move on.
