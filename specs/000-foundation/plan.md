---
status: approved
spec: ./spec.md
---

# 000 — Implementation plan

> Agent-drafted against the spec, human-approved. Written in a separate session, after
> the spec is approved. Do not write this file at the same time as the spec.

## Approach

One binary, three internal packages, one test-only package, and the local gate. The
binary is `main.go` plus a testable `run` function. `main.go` does wiring only. `run`
owns ordering, logging and exit codes, and tests call it directly.

### Entry point and exit codes
`cmd/gateway/main.go` builds a signal context, hands the real dependencies to `run`
and exits with its code:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
context.AfterFunc(ctx, stop) // a second signal gets Go's default behaviour and kills the process
code := run(ctx, deps{args: os.Args[1:], lookupEnv: os.LookupEnv, stdout: os.Stdout, listen: net.Listen})
stop()
os.Exit(code)
```

`run(ctx, deps) int` lives in `cmd/gateway/run.go`, in package `main`. It does this in
order:
1. Build the bootstrap logger (zap, JSON, `info`, to `deps.stdout`).
2. Parse flags with `flag.NewFlagSet(..., flag.ContinueOnError)` and
   `SetOutput(io.Discard)`, so the `flag` package never prints usage text. A flag
   error is logged as one JSON line. Exit `2`.
3. Call `config.Load`. On error, log one JSON line from `*config.Error`'s fields.
   Exit `2`. `listen` has not been called yet.
4. Build the real logger at the configured level.
5. `ln, err := deps.listen("tcp", cfg.ListenAddr)`. The address is already
   well-formed (config step 5), so this fails only at the OS level. On error, log
   `cannot bind` with the key and a fixed reason.
   `errors.Is(err, syscall.EADDRINUSE)` gives `address in use`; anything else gives
   `bind failed`. Exit `1`.
6. `srv := server.New(log, deps.mount...)`. Start `srv.Serve(ln)` in a goroutine.
7. Log the startup line: `version`, `addr` (`ln.Addr().String()`), `config_source`,
   `env_overrides`.
8. Wait for `ctx.Done()` or a `Serve` error. A `Serve` error other than
   `http.ErrServerClosed` is logged. Exit `1`.
9. `srv.Shutdown(ctx2)`, where `ctx2` is a fresh `context.WithTimeout(context.Background(),
   cfg.ShutdownTimeout)` (the signal context is already cancelled at this point). On
   `context.DeadlineExceeded`, log one error line `shutdown timed out` with
   `in_flight: srv.InFlight()`, call `srv.Close()`, and exit `1`. Otherwise log
   `gateway stopped` and exit `0`.

| Exit | Meaning |
|---|---|
| `0` | clean shutdown |
| `1` | runtime failure: bind, serve, or shutdown timeout |
| `2` | bad flags or invalid config (nothing was bound) |

`deps` holds everything a test needs to swap:

```go
type deps struct {
    args      []string
    lookupEnv func(string) (string, bool)
    stdout    io.Writer
    listen    func(network, addr string) (net.Listener, error)
    mount     []func(chi.Router) // test-only routes; nil in main
}
```

`version` is a package-level `var version = "dev"` in `main`, set with
`-ldflags "-X main.version=..."`.

**What still needs the built binary.** Real signal delivery. The subprocess test
builds `./cmd/gateway`, starts it on `127.0.0.1:0`, reads the startup line from
stdout to learn the port, calls `/healthz`, sends SIGTERM (and in a second case
SIGINT), and asserts exit `0`, the `gateway stopped` line, JSON on every stdout line,
and an empty stderr. That covers AC15's signal path end to end.

AC16 is **not** run as a subprocess. It needs a handler that hangs, and a built binary
could only have one by shipping a debug route or by using a build-tagged variant of the
binary. Both are worse than the gap they close. AC16 runs in-process through `run`
by cancelling `ctx`. AC15's subprocess test already proves that SIGTERM cancels that
same `ctx`.

### Listener
`net.Listen` first, then `srv.Serve(ln)`, never `ListenAndServe`. The startup line logs
`ln.Addr()`. Tests pass `GATEWAY_LISTEN_ADDR=127.0.0.1:0` and read the real port
from the startup line.

AC5 is asserted **without binding 7197** in a test. The injected `listen` records
the address it was asked for (it must be `127.0.0.1:7197` with no config), then
returns a `127.0.0.1:0` listener. A test that binds the real 7197 would fail
`make verify`, and so block every push, whenever `make run` or compose is running.
AC2's manual check covers the real bind.

### In-flight count (AC16)
`internal/server` has an `inflight` middleware: an `atomic.Int64`, `Add(1)` on entry
and `Add(-1)` in a `defer`. It is the outermost middleware, so it counts a request
from the first byte handled to the last, including one that panics. `Server.InFlight()`
reads it. After `Shutdown` returns `context.DeadlineExceeded`, `run` reads it once for
the error line. The count is handlers still running, not open connections. That is
what the spec's "in-flight requests" means.

### Test-only routes
`server.New(log *zap.Logger, mount ...func(chi.Router)) *Server` builds the router,
registers `/healthz`, and then calls each `mount` on it. `main` passes none, so the
shipped binary has exactly one route. Tests pass their own: `/hang` (blocks until the
request context ends) for AC16, `/slow` (sleeps 200 ms) for AC15 in-process, and
`/panic` for AC17. All middleware wraps them, so they are tested under the same chain
as production routes.

### Config: unknown keys, lines, and no values
`config.Load(Options{Path, DefaultPath, LookupEnv})` returns
`(Config, Source, error)`. All errors are `*config.Error{Key, Source, Reason, Line}`,
with no wrapped parser error, so the caller has nothing unsafe to log.

1. **Pick the file.** Use `-config` if set; a missing explicit path is an error
   (`file not found`). Otherwise use `./config.yaml` if it exists, or else no file.
   A file that exists but can't be read, on either path, gives `cannot read file`
   (research Q5).
2. **Parse into `yaml.Node`** with `yaml.Unmarshal`. An empty or comment-only file
   yields a zero node (`Kind == 0`, no error; checked in v3.0.5). That counts as an
   empty document: no keys set, not an error. A syntax error from yaml.v3 is an
   untyped error of the form `yaml: line N: <problem>` (checked in v3.0.5 `decode.go`
   `parser.fail`). A regexp `^yaml: line (\d+):` pulls out `N` and **everything else is
   discarded**. With no match, the error line carries only the path. Reason:
   `malformed yaml`.
3. **Walk the root mapping.** An empty document (the zero node from step 2, or a root
   that is a `!!null` scalar, as from a bare `---` or `~`) means "no keys set", and the
   walk is skipped (research Q3). A root that is not a mapping gives
   `not a mapping`. For each key node: unknown key gives
   `unknown key`, with `Key` and `Line` from the key node. A key seen twice gives
   `duplicate key` (yaml.v3 only checks for duplicates when decoding into a map or
   struct, not into a `Node`). A value node that is not a scalar gives `invalid type`.
   The known set comes from the `yaml` tags on the file struct, so it cannot drift from
   the struct.
4. **Decode with `KnownFields(true)`** into a struct of `*string` fields. It runs on a
   `yaml.NewDecoder` over the file's **raw bytes**, not on the node from step 2:
   `(*Node).Decode` has no `KnownFields` option. This is the decoder the spec names,
   and it stays as a backstop for the nested keys later features will add. An
   `io.EOF` on the first `Decode` means an empty stream (empty or comment-only file),
   so every key keeps its default. A key with a null value decodes to nil and keeps its
   default too. After step 3 it still fails on a scalar the struct can't hold, like
   `log_level: !!int abc`, whose message quotes the value; the error is reduced to
   `invalid config` plus the path (research Q3). A second `Decode` must
   return `io.EOF`; otherwise the reason is `multiple documents`.
5. **Validate the values ourselves.** Only the key, the source and a fixed reason reach
   the `Error`.
   - `shutdown_timeout`: `time.ParseDuration`. A parse error, or a result of zero or
     less (`0s`, `-5s`), gives `invalid duration`. A zero timeout would make every
     shutdown an immediate timeout.
   - `log_level`: one of `debug`, `info`, `warn`, `error` (case-insensitive). Anything
     else gives `invalid level`.
   - `listen_addr`: `net.SplitHostPort` must succeed, the host must not be empty, and
     the port must parse with `strconv.ParseUint(port, 10, 16)`, an integer from 0 to
     65535. Otherwise the reason is `invalid address`. An empty host (`:7197`) would
     bind every interface; that must be written out as `0.0.0.0` or `[::]`. Port `0` stays valid, because tests bind `127.0.0.1:0`.
     Named ports (`:http`) are rejected by the integer rule. This fails fast with the
     other config errors, instead of at bind.
6. **Env.** For each key, `LookupEnv(GATEWAY_...)`. Set and non-empty overrides the value,
   and the key name goes into `Source.EnvOverrides`. Set but empty counts as unset
   (ADR 0002). Env values are validated by the same functions, with `Source` set to the
   env var's name.

`Source` is `{File string /* "" = defaults */, EnvOverrides []string}`. The startup
line logs `config_source` (`defaults` or the path) and `env_overrides` (key names, and
always an array, so an empty one is `[]`).

### Logging
`internal/logging`:
- `New(w io.Writer, level string) *zap.Logger` builds a `zapcore.NewCore` with a JSON
  encoder whose keys are `level`, `ts` (RFC 3339 nano) and `msg`, plus `caller`. It
  does not attach stack traces to error lines by itself. The only stack is the one the
  recover middleware adds, as a JSON field.
- `Bootstrap(w)` is `New(w, "info")`.
- `StdLog(l)` is `zap.NewStdLogAt(l.Named("http"), zap.WarnLevel)` for
  `http.Server.ErrorLog`.
- zap's own internal errors (for example, a failed write) go to `ErrorOutput`. That is
  stderr in plain text by default. It is set to a small `WriteSyncer` that wraps each
  line as `{"level":"error","ts":...,"msg":"logger error","detail":"..."}` on stderr.
- No `zap.ReplaceGlobals`, `zap.L()` or `zap.S()`. Loggers are passed in. Lint enforces
  this (see Files).

### HTTP middleware (outermost first)
1. `inflight`: the counter above.
2. `requestID`: `crypto/rand.Text()` (stdlib since Go 1.24, 128 bits, base32, 26
   characters). No new dependency. It ignores any incoming `X-Request-Id`, sets the
   response header before calling `next`, and stores the ID in the context under a
   private key. chi's `middleware.RequestID` is not used because it trusts the incoming
   header (v5.3.2 `request_id.go`). The incoming header is left in `r.Header` untouched,
   because passthrough (001) is 001's call.
3. `accessLog`: wraps the writer with chi's `middleware.NewWrapResponseWriter`, which
   keeps `http.Flusher` for 001's streams. After `next` it logs `request` at `info` with
   `request_id`, `method`, `path` (`r.URL.Path` only, never the query, because some
   providers put keys there), `status`, `bytes`, `duration_ms` and `remote_addr`. It
   logs no headers and no body.
4. `recoverer`: `defer recover()`. `http.ErrAbortHandler` is re-panicked so `net/http`
   handles it silently, as it does by default. Any other panic logs `panic recovered`
   at `error` with `request_id`, `panic` (the value) and `stack` (a JSON string field).
   It writes `500` only if nothing has been written yet.

Routes: `GET /healthz` gives `200`, `Content-Type: application/json` and
`{"status":"ok"}`.

### Local gate
**`make setup`** runs `setup-git` then `setup-lint`, plus `go mod download`.
- `setup-git`: `git config core.hooksPath .githooks`,
  `git config notes.rewriteRef refs/notes/commits`, and the notes fetch refspec, added
  only if `git config --get-all remote.origin.fetch` doesn't already list it.
- `setup-lint`: if `./bin/golangci-lint version` already reports the pinned version, do
  nothing. Otherwise run the upstream install script **from the pinned tag's URL**
  (`raw.githubusercontent.com/golangci/golangci-lint/v$(GOLANGCI_LINT_VERSION)/install.sh`),
  which checks the release checksums, into `./bin`.

**`.githooks/pre-push`** is POSIX `sh`, like `commit-msg`, and reads the stdin lines:
- The branch filter is on the **remote ref**, not the local one. `git push origin
  HEAD:main` has local ref `HEAD`, but the remote ref is `refs/heads/main`. Lines whose
  remote ref is not `refs/heads/*` are skipped, and so are deletions.
- For each branch line, check that the local sha is `HEAD`, collect its range (as the
  spec defines it), and run `.githooks/commit-msg` on each commit's
  `git log -1 --format=%B` in a `mktemp` file.
- If at least one branch line remains: `git update-index -q --refresh`, then
  `git diff-index --quiet HEAD --` for the clean-tree check, then `make verify` **once**,
  run as `MAKEFLAGS= MFLAGS= GNUMAKEFLAGS= GOFLAGS= make verify`. Flags inherited
  from the pusher's environment, like make's `-i` or go's `-run=^$`, would otherwise
  make a failing verify exit 0 (research Q2).
- It stops on the first failure with a plain message on stderr. That rule is for the
  gateway; git hooks talk to a human. It never writes to the tree.

## Files and packages touched

| Path | Why |
|---|---|
| `go.mod`, `go.sum` | Module `github.com/brutally-honest/llm-gateway`, `go 1.25.4`, pins for chi, zap, yaml |
| `cmd/gateway/main.go` | Signal context and `os.Exit(run(...))` only |
| `cmd/gateway/run.go` | `run`, `deps`, `version`: the ordering and exit codes |
| `cmd/gateway/run_test.go` | In-process ACs through `run` |
| `cmd/gateway/binary_test.go` | Subprocess: builds the binary, real signals |
| `internal/config/` | Types, `Load`, `Error`. Pure: no zap, no net/http, so it is table-testable alone |
| `internal/logging/` | zap construction, `StdLog` adapter, JSON error output. The one place that knows zap's config |
| `internal/server/` | `Server`, router, middleware, `/healthz`. The HTTP surface later features mount on |
| `test/githooks/` | Go tests for the pre-push hook and `setup-git` (package holds tests only, see Testing). Go ignores `.githooks/` because of the leading dot, so the tests cannot live next to the hooks |
| `.githooks/pre-push` | New |
| `Makefile` | `setup` (`setup-git`, `setup-lint`), `build`, `run`, `test`, `lint`, `verify`, `image` |
| `.golangci.yml` | v2 format (`version: "2"`). forbidigo bans `os.Stdout`/`os.Stderr` except in `cmd/gateway/main.go` and `internal/logging/` |
| `Dockerfile`, `.dockerignore` | Multi-stage static build. `.dockerignore` keeps out `.git`, `bin/`, `config.yaml` and `.env*` |
| `docker-compose.yml` | Gateway service only |
| `config.example.yaml` | The three keys live (not commented out) at their defaults, each with a comment. `TestLoad_ExampleFileIsDefaults` needs real values to catch drift |
| `.gitignore` | Adds `/config.yaml` (root only, so a future `testdata/config.yaml` is not ignored) |
| `README.md` | Setup section points to `make setup` |
| `AGENTS.md` | `install: make setup`, `verify: make verify`, `run: make run` |

`internal/core/`, `protocols/`, `clients/` and `providers/` are not created.

**Makefile specifics.**
- `VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)`.
- `build`: `CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/gateway ./cmd/gateway`.
- `run`: `build`, then `./bin/gateway`.
- `image`: `VERSION=$(VERSION) docker compose build`. This is how "the Makefile passes it
  into the Docker build as a build arg" is met. Compose declares
  `args: { VERSION: ${VERSION:-dev} }`, so a bare `docker compose up --build` still
  works and reports `dev`.

**`.golangci.yml`**: the `standard` linter set (errcheck, govet, ineffassign,
staticcheck, unused), plus `errorlint`, plus `forbidigo`. forbidigo bans
`fmt.Print*`, `print`, `println`, `log.Print*|Fatal*|Panic*|Default|New*|SetOutput`,
`os.Stdout`, `os.Stderr`, `zap.L`, `zap.S` and `zap.ReplaceGlobals` outside
`_test.go` files. `cmd/gateway/main.go` and `internal/logging/` may use `os.Stdout`
and `os.Stderr`: that exclusion is scoped by issue text to those two names, so every
other ban, the zap globals included, still applies there. That turns "nothing writes
plain text" and "no global logger" into lint failures. The `gofmt` formatter is
enabled.

**Dockerfile**: builder `golang:1.25.4-trixie` (with a comment pointing at `go.mod`),
`ARG VERSION=dev`, `CGO_ENABLED=0`, and the same `-trimpath -ldflags` as `make build`.
Runtime is `gcr.io/distroless/static-debian13:nonroot`, running as `USER nonroot`
with `ENTRYPOINT ["/gateway"]`. **Compose**: `ports: ["127.0.0.1:7197:7197"]` and
`environment: GATEWAY_LISTEN_ADDR: 0.0.0.0:7197`.

### Pinned versions (checked 2026-09-23 against proxy.golang.org, gcr.io and Docker Hub)

| What | Version | Pinned in |
|---|---|---|
| Go | `1.25.4` (the local toolchain) | `go.mod` `go` line; Dockerfile builder tag |
| chi | `github.com/go-chi/chi/v5 v5.3.2` (2026-08-20, needs `go 1.23`) | `go.mod` |
| zap | `go.uber.org/zap v1.28.0` (2026-04-28). Pulls in `go.uber.org/multierr`, which is part of zap and not a new direct dependency | `go.mod` |
| yaml | `go.yaml.in/yaml/v3 v3.0.5` (2026-07-26) | `go.mod` |
| golangci-lint | `v2.13.2` (2026-08-27). Config format v2: `version: "2"` at the top of `.golangci.yml` | `Makefile` `GOLANGCI_LINT_VERSION`; format in `.golangci.yml` |
| Builder image | `golang:1.25.4-trixie@sha256:a02d35efc036053fdf0da8c15919276bf777a80cbfda6a35c5e9f087e652adfc` | `Dockerfile` |
| Runtime image | `gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3` | `Dockerfile` |

Images are pinned by tag and digest, so the tag says what it is and the digest says
exactly which build it is. debian13 is chosen over debian12 because it is the current
distroless line, rebuilt more recently.

## Interfaces introduced or changed

```go
// internal/config
type Config struct {
    ListenAddr      string
    LogLevel        string        // one of debug, info, warn, error (lower-cased)
    ShutdownTimeout time.Duration
}
type Options struct {
    Path        string                        // -config; "" = look for DefaultPath
    DefaultPath string                        // "config.yaml"
    LookupEnv   func(string) (string, bool)
}
type Source struct {
    File         string   // "" means defaults
    EnvOverrides []string // key names, never values
}
type Error struct {
    Key    string // "" for file-level errors (malformed yaml)
    Source string // file path or env var name
    Reason string // fixed text: "unknown key", "invalid duration", ...
    Line   int    // 0 if not applicable
}
func (e *Error) Error() string // built only from the fields above
func Defaults() Config
func Load(Options) (Config, Source, error)

// internal/logging
func New(w io.Writer, level string) *zap.Logger
func Bootstrap(w io.Writer) *zap.Logger
func StdLog(l *zap.Logger) *log.Logger

// internal/server
type Server struct{ /* http.Server, inflight atomic.Int64 */ }
func New(log *zap.Logger, mount ...func(chi.Router)) *Server
func (s *Server) Serve(ln net.Listener) error
func (s *Server) Shutdown(ctx context.Context) error
func (s *Server) Close() error
func (s *Server) InFlight() int64
func RequestID(ctx context.Context) string // for later features' log lines
```

No `ReadTimeout` or `WriteTimeout` is set on `http.Server`: a write timeout would cut
001's streams. `ReadHeaderTimeout: 10s` is set (slowloris), and it does not affect
streaming responses.

## Testing strategy

All automated tests run under `make verify` (`go test -race ./...`). In-process tests
read log lines from a goroutine-safe buffer. Each line is parsed as JSON, and a line
that isn't JSON fails the test.

**Hook tests** (`test/githooks`, Go). Each test builds a world in `t.TempDir()`:
- a bare `remote.git` added as `origin`, and a work repo;
- copies of `.githooks/commit-msg` and `.githooks/pre-push`;
- a **stub Makefile** whose `verify` target is `@exit $${STUB_VERIFY_EXIT:-0}`.

This stub is what breaks the recursion. The real pre-push hook runs `make verify` in
*this* repo. That runs these tests, which push from the temp repo, and the temp repo's
hook runs the stub's `verify`, not `go test`. The hook needs no env switch or bypass:
it always runs `make verify` in its own repo root. "After `make setup`" is reproduced by
running the **real** Makefile's `setup-git` target against the temp repo
(`make -f <abs>/Makefile -C <tmp> setup-git`). `setup-lint` is not run, because it needs
the network.

The child processes get a clean environment:
- every inherited `GIT_*` variable is removed (an outer `git push` exports some);
- `MAKEFLAGS`, `MFLAGS`, `GNUMAKEFLAGS` and `MAKELEVEL` are removed, because an outer `make -i`
  would otherwise make the stub's failing `verify` pass (research Q1);
- `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1` and `HOME=<tmp>`, so the
  user's global config (hooks, signing, templates) can't leak in;
- `user.name` and `user.email` are set locally.

These tests run inside `make verify`. They cost well under a second each, and `sh`,
`git` and `make` are already needed to use the repo at all.

**Config tests that no single AC names** (all in `make verify`):
- `internal/config` `TestLoad_ErrorsNeverContainValue` covers every reason in Config
  steps 1–5, so adding a reason without a test case is a test gap. Bind reasons
  (`address in use`, `bind failed`) stay out.
- `internal/config` `TestLoad_EmptyFile`: an empty file and a comment-only file load as
  `Defaults()`, with `Source.File` set to the path.
- `internal/config` `TestLoad_ExampleFileIsDefaults`: `config.example.yaml`, read from
  the repo root and copied verbatim into a temp dir, loads as `Defaults()` with
  `Source.File` set to the path. This keeps the example file in step with the defaults.
- `cmd/gateway` `TestRun_EmptyConfigFile`: an empty `config.yaml` and a verbatim copy of
  `config.example.yaml` each start the gateway on the defaults, and the startup line's
  `config_source` is the path, not `defaults`.
- `internal/config` `TestLoad_InvalidDuration`: `abc`, `0s` and `-5s`, from the file and
  from env, each give `invalid duration` naming the key and source.
- `internal/config` `TestLoad_InvalidAddress`: `localhost` (no port), `:7197` (empty
  host), `:http` (named), `127.0.0.1:-1`, `127.0.0.1:65536` and `127.0.0.1:99999`
  give `invalid address`. `127.0.0.1:0`, `0.0.0.0:7197`, `[::1]:7197` and
  `0.0.0.0:65535` load. Each is checked from the file and from env.
- `cmd/gateway` `TestRun_InvalidAddressFailsBeforeBind`: exit `2`, `listen` is never
  called, and the value is absent from the output.
- `cmd/gateway` `TestRun_BindErrors`: an injected `listen` returning an error that
  wraps `syscall.EADDRINUSE` gives exit `1` and reason `address in use`; any other
  error gives exit `1` and reason `bind failed`. The address appears nowhere in the
  output.

**Logging tests** (`internal/logging`, in `make verify`):
- `TestNew_JSONShape`: the keys are `level`, `ts`, `msg` and `caller`; the level
  filter works; there is no stack on error lines.
- `TestErrorOutput_JSON`: a zap internal error (a failing `WriteSyncer`) produces one
  JSON line with `msg` `logger error`, and no plain text.

### AC evidence

| AC | Evidence |
|---|---|
| AC1 | Manual: `make build && file bin/gateway` shows `statically linked` |
| AC2 | Manual: `make run`, then in another shell `curl -si http://127.0.0.1:7197/healthz` gives `200` and `{"status":"ok"}`. Automated half: `internal/server` `TestHealthz` |
| AC3 | `cmd/gateway` `TestRun_StartupLine` |
| AC4 | `TestRun_DefaultsWhenNoConfig` (empty temp cwd via `t.Chdir`, no env, recording `listen`) |
| AC5 | `TestRun_DefaultListenAddr` (the recorded `listen` address is `127.0.0.1:7197`) and `internal/config` `TestDefaults`. The real bind is AC2's manual check |
| AC6 | `TestRun_EnvOverridesFile`: a table over the three keys, each asserting the value in effect and `env_overrides` |
| AC7 | `TestRun_LogLevel`: a table of file `error` / env `error` / file `info` + env `error`, asserting the startup `info` line is absent or present |
| AC8 | `TestRun_UnknownKey` (exit `2`, `listen` never called, the line names the key and path) and `internal/config` `TestLoad_UnknownKey` (key + line) |
| AC9 | `TestRun_InvalidEnvLogLevel` |
| AC10 | `TestRun_InvalidEnvShutdownTimeout` |
| AC11 | The AC8–AC10 tests use a sentinel value and assert it is absent from all output. Plus `internal/config` `TestLoad_ErrorsNeverContainValue`, a table over every reason, including malformed YAML with the sentinel on the bad line |
| AC12 | `TestRun_EmptyEnvIsUnset`: with and without a file value |
| AC13 | `TestBinary_SignalShutdown`: every stdout line is JSON with `level`, `ts` and `msg`; the `request` line has `request_id`; stderr is empty. Plus in-process `TestRun_AllLinesJSON` |
| AC14 | `TestRun_SecretsNotLogged`: sentinel in `Authorization`, `x-api-key` and `?key=`, on `/healthz` and on a 404 path |
| AC15 | In-process `TestRun_GracefulShutdownWaitsForInFlight` (the `/slow` request completes with `200` after `ctx` is cancelled; exit `0`; `gateway stopped` logged). Subprocess `TestBinary_SignalShutdown` (SIGTERM and SIGINT cases) |
| AC16 | In-process `TestRun_ShutdownTimeout`: `/hang`, `shutdown_timeout: 100ms`, exit `1`, exactly one error line with `in_flight: 1`, no `gateway stopped` |
| AC17 | `TestRun_PanicRecovered`: `/panic` gives `500` with one `panic recovered` JSON line, then `/healthz` still gives `200`. `os.Stderr` is swapped for a pipe in this test (not parallel), and the pipe must be empty. Plus `internal/server` `TestRecoverer_HeadersAlreadyWritten` |
| AC18 | `internal/server` `TestRequestID`: the header is present, 26 characters, differs from the sent `X-Request-Id`, and differs between requests |
| AC19 | Manual. Pass: `make verify; echo $?` gives `0`. Test failure: `printf 'package config\nimport "testing"\nfunc TestZZFail(t *testing.T) { t.Fail() }\n' > internal/config/zz_fail_test.go; make verify; echo $?; rm internal/config/zz_fail_test.go` gives non-zero. Lint failure: `printf 'package config\nfunc zzUnused() {}\n' > internal/config/zz_lint.go; make verify; echo $?; rm internal/config/zz_lint.go` gives non-zero |
| AC20 | `test/githooks` `TestPrePush_Rejects/{malformed_subject,verify_fails,dirty_tree,sha_not_head}` |
| AC21 | `TestPrePush_Allows/{branch_deletion,clean_range,notes,tag}` |
| AC22 | `TestSetupGit_Idempotent`: `git config --local --list` is byte-identical after the first and second runs, and the notes refspec appears once |
| AC23 | Manual: `make image && docker compose up -d && curl -si http://127.0.0.1:7197/healthz && docker compose port gateway 7197 && docker compose down`. Expect `200` and `127.0.0.1:7197`. Stop `make run` first (see the note in `tasks.md`) |
| AC24 | Manual: `git clone <remote> /tmp/llmgw-fresh && cd /tmp/llmgw-fresh && make setup && make verify && make run` (then the AC2 curl) |

## Risks and unknowns

- **Output the runtime writes without zap.** An unrecovered panic outside a handler
  goroutine prints a plain-text trace to fd 2 and kills the process. That includes a
  goroutine a handler starts, because the recover middleware only covers the handler's
  own goroutine. So do `fatal error:` (concurrent map writes, out of memory, stack
  overflow), a SIGQUIT goroutine dump, and race-detector reports in test builds. None
  of these can be made JSON: the runtime writes straight to the file descriptor and
  exits. **How this squares with the spec:** "nothing writes plain text" is held for
  every path the gateway controls. That means handler panics (recover middleware),
  `net/http`'s own messages (`ErrorLog`), `flag` usage text (discarded), and zap's
  internal errors (wrapped to JSON). The whole codebase is covered by lint. Runtime crash
  output is the one exception, because it only appears when the process is already
  dying. `debug.SetCrashOutput` is **not** used to hide it: a crash is exactly what
  should stay visible. The panic-in-a-spawned-goroutine case matters to 001, whose
  streaming code may start goroutines.
- **Shutdown vs long streams.** The 30s `shutdown_timeout` default will cut SSE
  streams that run longer. This is the spec's open question, deferred to 001 (spec.md
  §Open questions). Nothing here pre-empts it: `Shutdown` + the in-flight count is the
  mechanism 001 will tune, and no `WriteTimeout` is set.
- **Parsing the syntax-error line depends on yaml.v3's message format.** It is
  `yaml: line N: ...` in v3.0.5, and v3 is frozen to security fixes (ADR 0002).
  `TestLoad_MalformedYAMLReportsLine` pins it. If the format changes, the error line
  loses the line number and never gains the value. For some errors (unclosed `[` or
  `{`) yaml.v3's line is the one before the bad line; it is passed through as is
  (research Q4).
- **Logged panic values.** The recover line logs the panic value. In 000 only our own
  code panics. In 001 a panic value could carry request data, so 001 should revisit this
  under PLAN §5's secrets rule.
- **Untracked files and `make verify`.** The clean-tree check covers tracked files only
  (spec). An untracked `_test.go` still gets compiled by `go test`, so `make verify` can
  pass or fail on code that is not pushed. This is accepted as the spec's trade-off.
- **`-race` needs cgo and a C compiler.** gcc 15.2 is installed here. A fresh machine
  without one fails `make test`, which AC24 would expose.
- **`make setup` needs the network** (install script plus `go mod download`). It can't
  complete offline.
- **The 30s `make verify` budget.** The subprocess test adds a `go build` (cached
  after the first run) and lint runs on a warm cache. To be measured when the gate
  lands. If it runs over, the subprocess build moves to `TestMain` and is shared.
- **Pinned digests age.** Security rebuilds of the images won't be picked up until the
  digests are bumped deliberately.

## Deviations from the spec

None
