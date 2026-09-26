---
status: approved
spec: ./spec.md
plan: ./plan.md
research: ./research.md
---

# 000 — Tasks

Small, ordered, checkable steps. The agent ticks them as it goes. A task held up by a
query in `research.md` ends with `(blocked: Q2)`; once that query is answered it becomes
`(shaped: Q2)`. The link stays on the task line either way — the status lives in
`research.md`.

> Note: AC2 (`make run`) and AC23 (`docker compose up`) both bind `127.0.0.1:7197`, so
> they can't run at the same time. Verify one, stop it, then verify the other.
>
> Don't push until T2 lands; the pre-push gate doesn't exist before it.

Each task is one commit. When it is done, `make verify` passes (from T1 on). Tests land
in the same task as the code they cover. "Done" names the proof, and the ACs a task
contributes to are listed at the end of its line. An AC whose evidence spans tasks is
listed on each of them.

## Gate

- [x] T1 — `go.mod` (`github.com/brutally-honest/llm-gateway`, `go 1.25.4`, no
  requires yet); `.gitignore` adds `/config.yaml`; `Makefile` with
  `GOLANGCI_LINT_VERSION = 2.13.2` and the targets `setup` (`setup-git`, `setup-lint`,
  `go mod download`), `test`, `lint` and `verify`; `.golangci.yml` (v2 format, the
  `standard` set, `errorlint`, `forbidigo` rules, `gofmt`); and
  `test/githooks/setup_test.go` with `TestSetupGit_Idempotent`. That test package is the
  module's first package, and `go test` and golangci-lint both need at least one to
  exit `0`. Done: `make setup && make verify` passes, and `TestSetupGit_Idempotent`
  passes. Commit: `chore(repo): add the make verify gate` (AC22)
- [x] T2 — `.githooks/pre-push` (remote-ref branch filter, `HEAD` check, range per the
  spec, `commit-msg` per commit, clean-tree check, then `make verify` once), and
  `test/githooks/prepush_test.go` (temp repo, bare `origin`, copied hooks, stub
  Makefile, clean `GIT_*` environment). From here on, every push goes through the real
  gate. Done: `TestPrePush_Rejects/{malformed_subject,verify_fails,dirty_tree,sha_not_head}`
  and `TestPrePush_Allows/{branch_deletion,clean_range,notes,tag}` pass under
  `make verify`. Commit: `chore(repo): add the pre-push hook` (AC20, AC21)
  (shaped: Q1)

## Gateway, bottom-up

- [x] T3 — `internal/config`: `Config`, `Options`, `Source`, `Error`, `Defaults`,
  `Load` (Config steps 1–6); adds `go.yaml.in/yaml/v3 v3.0.5` to `go.mod`. Also
  `config.example.yaml` (the three keys live at their defaults, not commented out,
  each with a comment), because `TestLoad_ExampleFileIsDefaults` in this task reads
  it and needs real values to catch drift. Done: `TestDefaults`,
  `TestLoad_UnknownKey`, `TestLoad_ErrorsNeverContainValue` (every reason in steps
  1–5), `TestLoad_MalformedYAMLReportsLine`, `TestLoad_EmptyFile`,
  `TestLoad_ExampleFileIsDefaults`, `TestLoad_InvalidDuration` and
  `TestLoad_InvalidAddress` pass. Commit: `feat(config): add the config loader`
  (AC5, AC8, AC11) (shaped: Q3, Q4, Q5)
- [x] T4 — `internal/logging`: `New`, `Bootstrap`, `StdLog`, and the JSON
  `ErrorOutput` writer; adds `go.uber.org/zap v1.28.0` to `go.mod`. Done:
  `TestNew_JSONShape` and `TestErrorOutput_JSON` pass, and `make verify` passes,
  including `forbidigo` over the new code. The zap-globals ban, carried over from T1
  because zap wasn't a dependency yet: a throwaway file calling `zap.L()`, `zap.S()`
  and `zap.ReplaceGlobals` gives 3 forbidigo findings in `internal/logging/` and 3 on
  any other path (deleted afterwards). Commit:
  `feat(logging): add the zap logger constructors` (shaped: Q6)
- [x] T5 — `internal/server`: `Server` (`New`, `Serve`, `Shutdown`, `Close`,
  `InFlight`, `ReadHeaderTimeout: 10s`), the `inflight`, `requestID`, `accessLog` and
  `recoverer` middleware, `RequestID(ctx)`, and `GET /healthz`; `server.New` builds the
  `http.Server` with `ErrorLog: logging.StdLog(log)`, so this task depends on T4's
  `internal/logging`; adds `github.com/go-chi/chi/v5 v5.3.2` to `go.mod`. Done:
  `TestHealthz`, `TestRequestID` and `TestRecoverer_HeadersAlreadyWritten` pass.
  Commit:
  `feat(server): add the http server with healthz` (AC2, AC17, AC18)
- [x] T6 — `cmd/gateway` happy path: `main.go` (signal context,
  `context.AfterFunc(ctx, stop)`, `os.Exit(run(...))`) and `run.go` (`deps`, `version`,
  flags, config, logger, `deps.listen`, `Serve`, the startup line with `version`,
  `addr`, `config_source` and `env_overrides`; on `ctx` cancel, `Shutdown` and
  `gateway stopped`, exit `0`); `Makefile` adds `VERSION`, `build` and `run`. `build`
  lands here, not in T1, because it builds `./cmd/gateway` and would fail before this
  task. Done: `TestRun_StartupLine`, `TestRun_DefaultsWhenNoConfig` and
  `TestRun_DefaultListenAddr` pass, and `make build` succeeds. Commit:
  `feat(server): add the gateway entry point` (AC3, AC4, AC5) (shaped: Q7)
- [x] T7 — Config and env paths through `run`: flag errors and `*config.Error` logged
  as one JSON line with exit `2` before `listen`; `-h` / `-help` logs one `usage`
  line and exits `0` before config loads; bind errors (`address in use`,
  `bind failed`) with exit `1`. Done: `TestRun_Help`, `TestRun_EnvOverridesFile`, `TestRun_LogLevel`,
  `TestRun_UnknownKey`, `TestRun_InvalidEnvLogLevel`,
  `TestRun_InvalidEnvShutdownTimeout`, `TestRun_EmptyEnvIsUnset`,
  `TestRun_EmptyConfigFile`, `TestRun_InvalidAddressFailsBeforeBind` and
  `TestRun_BindErrors` pass, and the AC8–AC10 tests find no sentinel in the output.
  Commit:
  `feat(server): fail fast on invalid config in run` (AC6, AC7, AC8, AC9, AC10, AC11,
  AC12) (shaped: Q7)
- [x] T8 — Logging, secrets and panics through `run`: `deps.mount` wired into
  `server.New`. Done:
  `TestRun_AllLinesJSON`, `TestRun_SecretsNotLogged` and `TestRun_PanicRecovered`
  (stderr piped and empty) pass. Commit:
  `feat(server): mount test routes through run` (AC13, AC14, AC17)
- [x] T9 — Graceful shutdown and timeout: `Shutdown` under a fresh
  `context.WithTimeout(context.Background(), cfg.ShutdownTimeout)`; on
  `context.DeadlineExceeded`, one `shutdown timed out` error line with
  `in_flight: srv.InFlight()`, then `srv.Close()` and exit `1`. Done:
  `TestRun_GracefulShutdownWaitsForInFlight` and `TestRun_ShutdownTimeout` pass.
  Commit: `feat(server): exit non-zero when shutdown times out` (AC15, AC16)
- [x] T10 — `cmd/gateway/binary_test.go`: builds `./cmd/gateway` into `t.TempDir()`,
  starts it on `127.0.0.1:0`, reads the port from the startup line, calls `/healthz`,
  and sends SIGTERM and SIGINT. Done: `TestBinary_SignalShutdown` passes (exit `0`,
  `gateway stopped`, every stdout line JSON, the `request` line has `request_id`,
  stderr empty). Commit: `test(server): add the signal shutdown binary test` (AC13,
  AC15)

## Container

- [x] T11 — `Dockerfile` (builder `golang:1.25.4-trixie` with a comment pointing at
  `go.mod`, `ARG VERSION=dev`, runtime `gcr.io/distroless/static-debian13:nonroot`, both
  with their digests from plan.md's Pinned versions table, in full), `.dockerignore`
  (`.git`, `bin/`, `config.yaml`, `.env*`), `docker-compose.yml` (gateway only,
  `127.0.0.1:7197:7197`, `GATEWAY_LISTEN_ADDR: 0.0.0.0:7197`,
  `args: { VERSION: ${VERSION:-dev} }`), and `Makefile` `image`. Done: `make image`
  succeeds, and `docker image inspect llm-gateway` shows `User` `nonroot`. The run
  check is T17. Commit: `build(deploy): add the docker image` (AC23)

## Docs

- [x] T12 — `README.md`: the Setup section points to `make setup` instead of listing the
  git config commands. Done: `grep -n 'make setup' README.md` shows it under Setup,
  and the section lists no `git config` lines. Commit:
  `docs(repo): point the readme setup at make setup`
- [x] T13 — `AGENTS.md`: `install: make setup`, `verify: make verify`,
  `run: make run`. Done: each runs as written in this repo. T18 proves them on a fresh
  clone. Commit: `docs(repo): fill the agents.md commands` (AC24)

## Manual evidence (no commit; output goes into the PR as evidence)

- [x] T14 — Run `make build && file bin/gateway`. It shows `statically linked`. Record
  the output. (AC1)
- [x] T15 — Run `make run`, then in another shell
  `curl -si http://127.0.0.1:7197/healthz`. It gives `200` and `{"status":"ok"}`.
  Record the startup line and the curl output. Stop the gateway before T17. (AC2)
- [x] T16 — Run each of these and record the exit codes:
  - `make verify; echo $?` gives `0`.
  - `printf 'package config\nimport "testing"\nfunc TestZZFail(t *testing.T) { t.Fail() }\n' > internal/config/zz_fail_test.go; gofmt -w internal/config/zz_fail_test.go; make verify; echo $?; rm internal/config/zz_fail_test.go`
    gives non-zero.
  - `printf 'package config\nfunc zzUnused() {}\n' > internal/config/zz_lint.go; make verify; echo $?; rm internal/config/zz_lint.go`
    gives non-zero.

  (AC19)
- [x] T17 — With `make run` stopped:
  `make image && docker compose up -d && curl -si http://127.0.0.1:7197/healthz && docker compose port gateway 7197 && docker compose down`.
  Expect `200` and `127.0.0.1:7197`. Record the output. (AC23)
- [x] T18 — `git clone -b chore/foundation <remote> /tmp/llmgw-fresh && cd /tmp/llmgw-fresh && make setup && make verify && make run`,
  then the T15 curl. Record the output. (AC24)
- [x] T19 — With a warm cache (run `make verify` once and discard it), time
  `time make verify` against the spec's 30s goal. Record the result only, as a numbered
  entry in `research.md` (Level: limit). If it is over 30s, add a new task T20 after
  this one for the `TestMain` change, with its own commit
  (`test(server): share the binary build in TestMain`), and note T20 in that entry's
  Outcome. Commit:
  `docs(repo): record the manual evidence and verify timing`
