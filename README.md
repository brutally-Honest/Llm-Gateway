# LLM Gateway

An observability-first LLM gateway in Go. It sits between any LLM client (Claude Code,
Cursor, …) and any LLM provider (Anthropic, OpenAI, …), forwards traffic faithfully,
and captures what flows through: prompts, responses, tool calls, tool results, tokens
and cost.

It is **not** a harness — it never runs agents, executes tools or edits files — and it
is not tied to one vendor.

Status: **Phase 0**, bootstrapping. No application code yet.

- `PLAN.md` — the one-page source of intent: goals, phases, stack rationale, open
  questions, commit conventions.
- `AGENTS.md` — the rules any agent working in this repo must follow.
- `specs/` — one folder per feature: `spec.md` (what and why), `plan.md` (how),
  `tasks.md` (checklist), `research.md` (doubts, roadblocks and limits, with answers).
- `docs/decisions/` — ADRs.
- `docs/clients/` — one page per client: how to connect, known limits.

## Setup

```sh
git config core.hooksPath .githooks          # commit-message conventions (PLAN.md §11)
git config notes.rewriteRef refs/notes/commits
```
