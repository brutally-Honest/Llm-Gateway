---
status: draft
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# NNN — Research

The feature's working memory: doubts, roadblocks and limits, with their answers.

What belongs here, when to escalate an answer to `spec.md`, `plan.md` or an ADR, and
the never-delete rule: `PLAN.md` §10 and `AGENTS.md`. Don't restate them here.

---

## Q1 — Do the hook tests need make's variables stripped, not only `GIT_*`?
- Status: answered     Level: technical
- Blocks / shapes: T1 (`cleanEnv`, already strips them), T2 (shaped)
- Context: plan.md's Testing strategy only removes `GIT_*` from the children's
  environment. But `make verify` runs `go test` under make, so `MAKEFLAGS`, `MFLAGS`
  and `MAKELEVEL` reach the `make verify` that the temp repo's hook runs against the
  stub Makefile. GNU Make 4.4.1, git 2.53.0, go 1.25.4.
- Question: does anything the outer make exports change what the stub's `verify`
  does, so that a hook test passes or fails for the wrong reason?
- Answer: yes. Experiment, 2026-09-26: with the three names taken out of `cleanEnv`'s
  strip list and `GOFLAGS=-count=1` (the test cache does not key on `MAKEFLAGS`, so a
  cached run proves nothing), `make -i test` fails
  `TestPrePush_Rejects/verify_fails` with "push succeeded, want it rejected": `-i`
  reaches the stub through `MAKEFLAGS` and its `exit 1` is ignored. `make -k`,
  `-j4` and `-s` pass. With the names stripped, `make -i test` and `make -j4 test`
  pass.
- Outcome: `cleanEnv` keeps stripping `MAKEFLAGS`, `MFLAGS` and `MAKELEVEL`, and
  plan.md's Testing strategy now lists them. The same leak reaches the real hook: see
  Q2.

## Q2 — Should the pre-push hook clear an inherited `MAKEFLAGS` before `make verify`?
- Status: open     Level: technical
- Blocks / shapes: nothing yet (T2's ACs pass as planned)
- Context: found during Q1. The hook runs `make verify` in whatever environment
  `git push` was started from. With a lint failure in the tree, 2026-09-26:
  `make verify` exits 2, but `MAKEFLAGS=i make verify` exits 0. So a push started from
  a shell or make recipe that exports `MAKEFLAGS` with `-i` gets through the gate
  with a failing check.
- Question: should the hook run `MAKEFLAGS= MFLAGS= make verify`, so the gate
  can't be relaxed from outside? That changes plan.md's hook description and needs a
  test (`TestPrePush_Rejects/verify_fails_makeflags`).
- Answer: pending
- Outcome: pending
