---
description: Run every remaining task of an approved feature, implement → review → fix, without stopping unless a stop rule fires.
argument-hint: <feature folder, e.g. 000-foundation>
allowed-tools: Bash(make verify) Bash(git status) Bash(git status *) Bash(git branch --show-current)
---

Feature: `specs/$ARGUMENTS/`

You are the orchestrator. You never write code or edit specs yourself: you only
dispatch subagents, relay their results, and keep a run log. Keep your own context
small. Pass file paths and short findings, not file contents.

## Preconditions (check once, stop if any fail)
- `spec.md`, `plan.md` and `tasks.md` all have `status: approved`.
- The working tree is clean and the current branch is not `main`.
- `make verify` passes before you start.

## Loop: for each unticked task in `tasks.md`, top to bottom
1. Dispatch **implementer** with: the feature folder and the task id. Nothing else.
2. If it returns `BLOCKED` or `NEEDS-HUMAN`: record it, then continue with the next
   task only if that task doesn't depend on this one (it's in a different section,
   or its line doesn't mention the blocked task's files). Otherwise stop.
3. If `DONE`, dispatch **reviewer** with: the feature folder, the task id, the task's
   line from `tasks.md` verbatim, and "review `git show <sha>`".
4. If `FIX`, dispatch a **new** implementer with the task id and the reviewer's
   findings verbatim, then review again. Maximum 2 fix rounds per task. A third
   `FIX` stops the run.
5. If `PASS`, go to the next task.

## Stop immediately when
- a reviewer returns `ESCALATE`;
- a task hits its fix-round limit;
- a blocked task is needed by the next one;
- `make verify` is red at the start of a task (the previous one broke something);
- the working tree is not clean at the start of a task (the previous one left
  something behind).

## When every task is done or skipped
Dispatch **reviewer** once more with "review `git diff main...HEAD` against the whole
spec: every AC, scope in both directions, checks on the checks."

Never push. Never merge. Never edit `spec.md` status.

## Final report (this is the only thing the human reads)
- Tasks: done / blocked (Qn) / needs-human, one line each with the sha.
- Fix rounds used per task, if any.
- Open queries in `research.md` that need an answer, quoted by number.
- The final branch review's findings.
- What the human must run by hand (from NEEDS-HUMAN).
