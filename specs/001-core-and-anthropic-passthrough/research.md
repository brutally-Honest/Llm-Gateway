---
status: draft
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# 001 — Research

The feature's working memory: doubts, roadblocks and limits, with their answers.

What belongs here, when to escalate an answer to `spec.md`, `plan.md` or an ADR, and
the never-delete rule: `PLAN.md` §10 and `AGENTS.md`. Don't restate them here.

---

## Q1 — Does Claude Code send `HEAD /api/hello` through the base URL, and when?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC9, AC13, AC33
- Context: 2026-09-26. A throwaway logging proxy (not in the repo) ran at
  `ANTHROPIC_BASE_URL=http://127.0.0.1:7198/anthropic`, forwarding to
  `api.anthropic.com`. An interactive Claude Code session (`claude-cli/2.1.283`,
  subscription/OAuth login) ran a tool-call prompt and `/model`, then quit. Only header
  names were logged, plus the values of safe headers. Earlier, a `claude -p` run in a
  different environment sent no `HEAD /api/hello`.
- Question: does Claude Code send `HEAD /api/hello` to `ANTHROPIC_BASE_URL` or straight
  to `api.anthropic.com`, and when?
- Answer: through the base URL, prefix kept: `HEAD /anthropic/api/hello`, once, at the
  start of the interactive session. It carries no auth (`auth=none`), and its only
  headers are `Accept`, `Accept-Encoding`, `Connection` and `User-Agent: Bun/1.4.3`.
  Not seen in `-p` mode.
- Outcome: the spec is unchanged: it already forwards `/api/hello` like any other path.
  The request has no `claude-cli/` User-Agent and no `X-App`, so the Claude Code
  profile labels it `unknown`. That is the correct fallback, not a bug.

## Q2 — Does Claude Code keep the base-URL path prefix on every path?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes the `/anthropic` mount
- Context: the same probe run as Q1, 2026-09-26.
- Question: with `ANTHROPIC_BASE_URL=http://127.0.0.1:7198/anthropic`, does every
  request keep the `/anthropic` prefix, including `/api/*`?
- Answer: yes. Every request kept it: `HEAD /anthropic/api/hello` and every
  `POST /anthropic/v1/messages?beta=true`.
- Outcome: none; this confirms the `/anthropic` prefix mount in spec.md's Scope.

## Q3 — Which paths does subscription mode call through the base URL?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC9, AC45
- Context: the same probe run as Q1, 2026-09-26. Subscription (OAuth) login, one
  tool-call prompt, `/model`.
- Question: does subscription mode call paths beyond `/v1/*` through the base URL (for
  example `/api/oauth/*`), and do they all work through a proxy?
- Answer: in this session, only `HEAD /api/hello` (Q1) and five
  `POST /v1/messages?beta=true`. No `/api/oauth/*` or other path went through the base
  URL, and `/model` made no API call (no `GET /v1/models`).
  - The messages requests carried `auth=bearer`,
    `User-Agent: claude-cli/2.1.283 (external, cli)` and `X-App: cli`.
  - Headers included `Anthropic-Beta`, `Anthropic-Version`, `Authorization`,
    `X-Claude-Code-Session-Id` and `X-Stainless-*`.
  - `Anthropic-Beta` always contained `oauth-2025-04-20`, but its flag list changed
    between requests: 8 flags on the first call, up to 15 later.
- Outcome: none to the spec. The beta list that changes per request is live evidence
  for the open-list header rule (PLAN.md §5): a header allowlist, or a value cached from
  the first request, would break Claude Code.

## Q4 — Does API-key mode behave the same through the gateway?
- Status: open     Level: flow
- Blocks / shapes: AC44
- Context: 2026-09-26. The probe in Q1–Q3 covered subscription mode only; no API key
  was available.
- Question: in API-key mode, which paths and headers does Claude Code send through the
  base URL, and does it behave as it does direct?
- Answer: untested.
- Outcome: to be run as part of the manual checks (AC44–AC46).
