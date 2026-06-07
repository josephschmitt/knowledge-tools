---
description: Consume answered judgment-call issues — read the comment thread, apply my decision to the wiki, and close the issue. The inbound half of the issue loop; pairs with /synthesize.
model: opus
effort: high
allowed-tools: "Bash(gh issue list:*), Bash(gh issue view:*), Bash(gh issue comment:*), Bash(gh issue close:*)"
---

The consumer side of the judgment-call loop. `/synthesize` files GitHub issues when it hits
something only I can decide; this command reads my answers back out and lands them in the
wiki. It is targeted, not a whole-corpus pass — only touch the issues and the notes they name.

## Find the open calls

List the open vault issues (two calls — `--label` flags AND together):

```
gh issue list --state open --label "vault:judgment-call"
gh issue list --state open --label "vault:needs-verification"
```

For each open issue, read the full thread including my replies:

```
gh issue view <number> --comments
```

## Decide what to do with each

An issue is resolved one of two ways. Anything else, **leave it open and untouched**.

1. **I answered it.** My comment gives an actionable decision (which note is current, what to
   retire, the verified fact). Apply that decision to the named notes:
   - Edit, merge, or retire the `wiki/` notes per my answer, written as established fact in the
     vault's voice and preserving the *why*. Fix any links and update `index.md` if structure
     changed.
   - Then close the issue with a one-line `gh issue comment` stating exactly what you changed
     (name the notes), followed by `gh issue close <number>`.
2. **The vault already moved on.** A later compile or synthesis made the issue's premise moot —
   the contradiction is gone, or the claim was since corrected — so there's nothing left to do.
   Close it with a comment noting it's resolved by the current state of the wiki, no edit needed.

**Do not guess.** If my reply is ambiguous, partial, or asks a follow-up, leave the issue open
and make no edits. You may add a `gh issue comment` noting precisely what's still unclear so the
next round has a sharper question, but don't act on an answer you're not sure of. An issue with
no reply from me yet is simply skipped.

## Closing

- Append a one-line, ISO-dated entry to `log.md` (newest at the bottom): which issues you
  applied and closed (with numbers), and what notes changed.
- **Do not** touch `inbox/` or `inbox/archive/`, and **do not** run git — leave the commit to me
  (or the tools-repo wrapper) so the wiki changes get reviewed. Closing issues is a side effect
  you do directly; it's separate from the commit.

End by telling me, briefly: which issues you closed and what you applied for each, and which open
issues you left untouched and why (waiting on me / ambiguous).
