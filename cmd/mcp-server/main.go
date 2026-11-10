// Command mcp-server runs an eino-ext MCP server for one tool family.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/urfave/cli/v3"

	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
	"github.com/webcenter-fr/eino-ext/libs/mcp/serverconfig"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cmd := &cli.Command{
		Name:  "mcp-server",
		Usage: "Run an eino-ext MCP server for one tool family",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "YAML configuration file"},
			&cli.StringFlag{Name: "server", Usage: "Tool family: kubernetes, argocd, prometheus or grafana"},
			&cli.StringFlag{Name: "transport", Usage: "MCP transport: stdio or http"},
			&cli.StringFlag{Name: "addr", Usage: "MCP HTTP listen address"},
			&cli.StringFlag{Name: "ops-addr", Usage: "Operations listen address (/healthz, /readyz, /metrics)"},
			&cli.StringFlag{Name: "log-level", Usage: "Log level"},
			&cli.StringFlag{Name: "log-format", Usage: "Log format: text or json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return serve(ctx, cmd)
		},
	}
	return cmd.Run(ctx, args)
}

func serve(ctx context.Context, cmd *cli.Command) error {
	// Configure early so config-loading diagnostics honour the CLI flags; the
	// file values are applied again after Load.
	if err := logging.Configure(cmd.String("log-level"), cmd.String("log-format")); err != nil {
		return err
	}

	cfg, err := serverconfig.Load(cmd.String("config"), overrides(cmd))
	if err != nil {
		return err
	}
	if err := logging.Configure(cfg.Log.Level, cfg.Log.Format); err != nil {
		return err
	}

	srv, err := serverconfig.Build(ctx, cfg)
	if err != nil {
		return err
	}

	switch cfg.Transport {
	case "stdio":
		return srv.ServeStdio(ctx)
	case "http":
		return serveHTTP(ctx, srv, cfg)
	default:
		return errors.Errorf("unsupported transport %q", cfg.Transport)
	}
}

func overrides(cmd *cli.Command) map[string]any {
	flags := []struct {
		flag string
		key  string
	}{
		{"server", "server"},
		{"transport", "transport"},
		{"addr", "addr"},
		{"ops-addr", "opsAddr"},
		{"log-level", "log.level"},
		{"log-format", "log.format"},
	}
	out := make(map[string]any, len(flags))
	for _, f := range flags {
		if cmd.IsSet(f.flag) {
			out[f.key] = cmd.String(f.flag)
		}
	}
	return out
}

func serveHTTP(ctx context.Context, srv *mcpserver.Server, cfg *serverconfig.Config) error {
	mcpHTTP := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.HTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	opsHTTP := &http.Server{
		Addr:              cfg.OpsAddr,
		Handler:           serverconfig.OpsHandler(func() error { return nil }),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		if err := mcpHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- errors.Wrap(err, "MCP HTTP server failed")
		}
	}()
	go func() {
		if err := opsHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- errors.Wrap(err, "operations HTTP server failed")
		}
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Shut the MCP listener down first so probes stay answerable while
	// in-flight requests drain.
	mcpErr := mcpHTTP.Shutdown(shutdownCtx)
	opsErr := opsHTTP.Shutdown(shutdownCtx)
	return errors.Combine(serveErr, mcpErr, opsErr)
}
