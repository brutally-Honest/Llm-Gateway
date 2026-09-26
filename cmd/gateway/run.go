package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"syscall"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// deps is everything run takes from the outside world, so tests can swap it.
type deps struct {
	args      []string
	lookupEnv func(string) (string, bool)
	stdout    io.Writer
	listen    func(network, addr string) (net.Listener, error)
	mount     []func(chi.Router) // test-only routes; nil in main
}

// Exit codes.
const (
	exitOK      = 0
	exitRuntime = 1 // bind, serve or shutdown failed
	exitConfig  = 2 // bad flags or invalid config; nothing was bound
)

// run starts the gateway and serves until ctx is cancelled. It returns the exit code.
func run(ctx context.Context, d deps) int {
	boot := logging.Bootstrap(d.stdout)

	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the flag package never prints usage text
	configPath := fs.String("config", "", "path to the config file (default ./config.yaml if present)")
	switch err := fs.Parse(d.args); {
	case errors.Is(err, flag.ErrHelp):
		// -h or -help: usage as one JSON line; nothing is loaded or bound.
		boot.Info("usage", zap.Strings("flags", []string{"-config <path>"}))
		return exitOK
	case err != nil:
		// The flag package's message can quote the value, so only the fact is logged.
		boot.Error("invalid flags")
		return exitConfig
	case fs.NArg() > 0:
		// The gateway takes no arguments; -config is the only way to name a file. A
		// positional one can be anything, a path included, so only the count is logged.
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", fs.NArg()))
		return exitConfig
	}

	cfg, src, err := config.Load(config.Options{Path: *configPath, DefaultPath: "config.yaml", LookupEnv: d.lookupEnv})
	if err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			// Built from the error's fields, never err.Error() of a parser (ADR 0002).
			fields := []zap.Field{
				zap.String("key", ce.Key),
				zap.String("source", ce.Source),
				zap.String("reason", ce.Reason),
			}
			if ce.Line > 0 {
				fields = append(fields, zap.Int("line", ce.Line))
			}
			boot.Error("invalid config", fields...)
		} else {
			boot.Error("invalid config")
		}
		return exitConfig
	}

	log := logging.New(d.stdout, cfg.LogLevel)

	ln, err := d.listen("tcp", cfg.ListenAddr)
	if err != nil {
		// The address is valid (config checked it), so this is the OS refusing. The
		// error names the address, so only a fixed reason is logged.
		reason := "bind failed"
		if errors.Is(err, syscall.EADDRINUSE) {
			reason = "address in use"
		}
		log.Error("cannot bind", zap.String("key", "listen_addr"), zap.String("reason", reason))
		return exitRuntime
	}

	srv := server.New(log, d.mount...)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	configSource := src.File
	if configSource == "" {
		configSource = "defaults"
	}
	log.Info("gateway started",
		zap.String("version", version),
		zap.String("addr", ln.Addr().String()),
		zap.String("config_source", configSource),
		zap.Strings("env_overrides", src.EnvOverrides),
	)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		log.Error("serve failed", zap.Error(err))
		return exitRuntime
	}

	// ctx is already cancelled, so the shutdown deadline needs a fresh context.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed")
		return exitRuntime
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve failed", zap.Error(err))
		return exitRuntime
	}
	log.Info("gateway stopped")
	return exitOK
}
