package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"

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
	if err := fs.Parse(d.args); err != nil {
		boot.Error("invalid flags")
		return exitConfig
	}

	cfg, src, err := config.Load(config.Options{Path: *configPath, DefaultPath: "config.yaml", LookupEnv: d.lookupEnv})
	if err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			boot.Error("invalid config",
				zap.String("key", ce.Key),
				zap.String("source", ce.Source),
				zap.String("reason", ce.Reason),
				zap.Int("line", ce.Line),
			)
		} else {
			boot.Error("invalid config")
		}
		return exitConfig
	}

	log := logging.New(d.stdout, cfg.LogLevel)

	ln, err := d.listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("cannot bind", zap.String("key", "listen_addr"))
		return exitRuntime
	}

	srv := server.New(log)
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
