---
status: draft
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# 001 — Research

The feature's working memory: every doubt, roadblock and limit met while building,
with its answer once found. Too detailed for the spec or the plan, worth remembering.

**What goes here**
- **Flow** — "does Claude Code send `count_tokens` before or after the first message?"
- **Low-level technical** — "`ReverseProxy` buffers when `Content-Length` is set — how
  to force a flush?"
- **Limits hit** — "Cursor strips our custom header; can't use it for the session ID."
- **Roadblocks and workarounds** — what blocked, and what unblocked it.

**Referenced only** from `tasks.md` (which task it blocked or shaped) and from commit
git notes (`Research: … (Q2, Q5)`). The spec and plan never link here; they stay at the
level of intent and approach.

**Escalate, so nothing important stays buried**

| If the answer… | Then |
|---|---|
| changes scope, acceptance criteria, or a do/don't | update `spec.md` itself (not a link), mark the query `→ spec` |
| changes the approach | update `plan.md` itself, mark the query `→ plan` |
| matters beyond this feature, or is hard to reverse | write an ADR, mark the query `→ ADR-NNNN` |
| is local to this feature | it stays here; that's the default |

**Entries are never deleted.** A wrong answer later gets a new dated line, not an edit,
so the history of the confusion is kept.

---

## Q1 — <FILL: one-line title of the doubt, roadblock or limit>
- Status: open | answered | workaround | won't fix     Level: flow | technical | limit
- Blocks / shapes: <FILL: T4, T6 — or "nothing yet">
- Context: <FILL: what you were doing and what you saw — logs, versions, commands>
- Question: <FILL: the one thing to know>
- Answer: <FILL: what's true, and how you know — test, doc link, experiment>
- Outcome: <FILL: what changed because of it; escalated → spec / plan / ADR-NNNN, if any>
