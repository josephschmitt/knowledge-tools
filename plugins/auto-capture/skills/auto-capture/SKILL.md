---
name: auto-capture
description: Proactively capture knowledge to Joe's vault inbox during ANY conversation — without being asked. Engage on your own initiative when the discussion produces something worth keeping — a durable fact, decision, preference, finding, link, snippet, or conclusion (knowledge), or a concrete thing Joe needs to do, follow up on, buy, fix, or decide (a task). Capture at the settle point — when a decision lands, a conclusion hardens, or the thread moves on — NOT turn-by-turn while options are still being weighed; a thread that never settles gets one capture of its open state at the end. The always-on counterpart to the knowledge-vault skill, which fires when Joe says "save this". Joe's over-capture rule governs whether something is worth keeping, not when — once settled, when in doubt, capture. Capture is raw via append_to_inbox, zero decisions; the compiler sorts, dedupes, and files later. Skip the trivial or transient; ask first only when something looks too private or sensitive to persist.
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

That bias is about **worth, not timing**. "When in doubt, capture" answers *is this worth
keeping* — it never means *capture before the thought is done cooking*. Timing has its own
rule (the settle point, below), and deferring to it loses nothing: the material is still
right there in the conversation when the thread lands.

You do not need permission for each capture. Capturing is the default behavior, not a thing
you stop to ask about.

## What's worth capturing

Capture proactively, as a rule of thumb, whenever the conversation produces:

- **Durable knowledge** — a fact Joe will want later, a decision he landed on, a preference
  or constraint he stated, a conclusion you reached together, a useful link or source, a
  snippet / quote / config worth keeping, a non-obvious "how X works" he just worked out.
- **A task** — something Joe needs to *do*: a follow-up, a thing to buy, a bug to fix, a
  person to contact, a decision to revisit — anything with a verb pointed at Joe.

Both go through the **same** tool, `append_to_inbox` — there's no separate task-vs-knowledge
field. The tool carries the rule for wording a task capture (lead with `TODO:`, include any
deadline) so the compiler files it as actionable rather than reference.

Lean toward capturing. Skip only the genuinely throwaway: small talk and passing chatter, a
fact already obviously and durably Joe's, something purely transient to the current turn (a
one-off calculation he won't need again) — or a position that's still *in motion* in the
current thread, which isn't a skip but a **wait** (next section).

## When to capture — the settle point, not every turn

Worth-it is only half the test; the other half is **timing**. The unit of capture is a
*settled* thread, not a turn. While Joe is still weighing pros and cons, comparing options,
or thinking out loud, do **not** capture each intermediate position — a mid-deliberation
capture is churn: it lands in the inbox only to be superseded two turns later, and the
compiler ends up reconciling drafts of a thought instead of the thought.

Capture when the material **hardens**:

- Joe lands on a decision ("let's go with X", "yeah, that's the one").
- A conclusion stops moving — the discussion starts *building on it* instead of revisiting it.
- The conversation moves on to a different topic, leaving the result behind.
- The conversation is clearly wrapping up.

A thread that never settles is still capture-worthy — an open question with its live options
and constraints is exactly the kind of in-progress thinking the vault's notebook exists for.
Capture it **once**, when the thread is abandoned or the conversation ends, framed as what it
is (the open question and where the thinking stands), not as a string of interim verdicts.

If something you already captured gets overturned later in the same conversation, capture the
new conclusion — the compiler reconciles supersessions. That's the safety net for a
settle-point misjudgment, not a license to capture every draft.

Facts are usually settled on arrival. This section is about *deliberation* — decisions,
comparisons, conclusions under discussion. A stable fact, link, or finding that surfaces
mid-conversation (an error message decoded, a source found, a constraint stated) is already
hardened and can be captured when it appears.

## How to capture

Drop it into the inbox raw with `append_to_inbox` and stop there — the tool's own rules apply
(capture takes zero decisions: no dup-searching, no judging worth past the bar above, no
categorizing or pre-organizing; the vault's compiler does all of that later with the whole
vault in view). Your job is to make the dump legible:

- **Capture the *content*, not the transcript.** Work out the actual durable thing — the
  decision, the link, the conclusion, the action — and capture that, not the back-and-forth
  that produced it. Fold any source URL and a line of what it is into the `text`.
- **One capture per distinct item.** If a turn surfaced two unrelated things, that's two
  `append_to_inbox` calls.

## Multiple vaults — always the default

Autonomous capture **never asks which vault** — asking is exactly the friction this skill
exists to avoid. If Joe runs several vaults (each its own connector), always file to the
**default**: the one connected as the bare `knowledge-vault` server (the `knowledge-vault-<label>`
variants are the non-default ones). Don't try to route by topic — that's a content decision, and
capture takes none. If Joe explicitly wants something in a specific non-default vault, that's a
"save this to work" — an explicit request the `knowledge-vault` skill handles, not this silent
path.

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
happens on the vault host on a schedule, under the vault's own `CLAUDE.md`. Querying the library and
answering judgment calls live in the `knowledge-vault` skill. Don't reproduce any of that
here. Capture raw, announce briefly, move on.
