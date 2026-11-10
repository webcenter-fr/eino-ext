// Command approval runs the standalone MCP approval service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/urfave/cli/v3"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/logging"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	approvalserver "github.com/webcenter-fr/eino-ext/server/approval"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/file"
	"github.com/webcenter-fr/eino-ext/server/approval/store/opensearch"
	"github.com/webcenter-fr/eino-ext/server/approval/store/postgresql"
	"github.com/webcenter-fr/eino-ext/server/approval/store/redis"
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
	return newCommand(serve).Run(ctx, args)
}

func newCommand(action cli.ActionFunc) *cli.Command {
	return &cli.Command{
		Name:  "approval",
		Usage: "Run the standalone MCP approval service",
		// Credential values contain commas (name=token@prod,staging); keep the
		// repeatable flag value intact instead of splitting it.
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "YAML configuration file"},
			&cli.StringFlag{Name: "addr", Value: defaultAddr, Usage: "HTTP listen address"},
			&cli.StringFlag{Name: "base-url", Usage: "public base URL (default derived from -addr)"},
			&cli.StringFlag{Name: "store", Value: defaultStore, Usage: "store backend: memory|file|redis|postgresql|opensearch"},
			&cli.StringFlag{Name: "store-dir", Value: defaultStoreDir, Usage: "file store directory"},
			&cli.StringFlag{Name: "redis-addr", Value: defaultRedisAddr, Usage: "Redis address"},
			&cli.StringFlag{Name: "postgres-dsn", Usage: "PostgreSQL DSN"},
			&cli.StringFlag{Name: "opensearch-url", Usage: "OpenSearch URL"},
			&cli.StringFlag{Name: "receipt-secret", Usage: "HS256 receipt secret"},
			&cli.StringFlag{Name: "receipt-key", Usage: "PEM receipt private key (RS256/EdDSA)"},
			&cli.StringFlag{Name: "receipt-alg", Value: defaultReceiptAlg, Usage: "receipt algorithm: HS256|RS256|EdDSA"},
			&cli.StringFlag{Name: "oidc-issuer", Usage: "OIDC issuer URL"},
			&cli.StringFlag{Name: "oidc-client-id", Usage: "OIDC client id"},
			&cli.StringFlag{Name: "oidc-client-secret", Usage: "OIDC client secret"},
			&cli.StringFlag{Name: "oidc-redirect-url", Usage: "OIDC redirect URL"},
			&cli.StringFlag{Name: "policy-file", Usage: "JSON approver policy file"},
			&cli.StringSliceFlag{Name: "server-credential", Usage: "server credential: name=token[@instance1,instance2] or name=sha256:hash"},
			&cli.StringFlag{Name: "receipt-secret-file", Usage: "file containing the HS256 receipt secret"},
			&cli.StringFlag{Name: "receipt-key-file", Usage: "file containing the PEM receipt private key"},
			&cli.StringFlag{Name: "oidc-client-secret-file", Usage: "file containing the OIDC client secret"},
			&cli.StringFlag{Name: "postgres-dsn-file", Usage: "file containing the PostgreSQL DSN"},
			&cli.StringFlag{Name: "server-credential-file", Usage: "file with one name=token[@instance,...] credential per line"},
			&cli.StringFlag{Name: "log-level", Usage: "log level"},
			&cli.StringFlag{Name: "log-format", Usage: "log format: text or json"},
		},
		Action: action,
	}
}

func serve(ctx context.Context, cmd *cli.Command) error {
	opts, err := loadOptions(cmd)
	if err != nil {
		return err
	}
	if err := logging.Configure(opts.logLevel, opts.logFormat); err != nil {
		return err
	}

	credentials, err := parseCredentials(opts.credentials)
	if err != nil {
		return err
	}
	policy, err := loadPolicy(opts.policyFile)
	if err != nil {
		return err
	}
	approvalStore, closeStore, err := selectStore(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeStore() }()

	cfg := &approvalserver.Config{
		ListenAddr:        opts.addr,
		BaseURL:           opts.baseURL,
		Store:             approvalStore,
		ServerCredentials: credentials,
		Receipt: approvalserver.ReceiptConfig{
			Secret:        opts.receiptSecret,
			PrivateKeyPEM: opts.receiptKey,
			Algorithm:     opts.receiptAlg,
		},
		Policy: policy,
	}
	if opts.oidcIssuer != "" {
		cfg.OIDC = &approvalserver.OIDCConfig{
			Issuer:       opts.oidcIssuer,
			ClientID:     opts.oidcClientID,
			ClientSecret: opts.oidcClientSecret,
			RedirectURL:  opts.oidcRedirectURL,
		}
	}
	srv, err := approvalserver.New(ctx, cfg)
	if err != nil {
		return err
	}

	go gcLoop(ctx, approvalStore)

	httpServer := &http.Server{
		Addr:              opts.addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return errors.Wrap(err, "approval server failed")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func gcLoop(ctx context.Context, s store.ApprovalStore) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_, _ = s.GC(ctx, now)
		}
	}
}

func selectStore(ctx context.Context, opts *options) (store.ApprovalStore, func() error, error) {
	switch opts.storeType {
	case "memory":
		return store.NewMemory(), func() error { return nil }, nil
	case "file":
		s, err := file.New(ctx, &file.Config{Dir: opts.storeDir})
		return s, func() error { return nil }, err
	case "redis":
		s, err := redis.New(ctx, &redis.Config{Addr: opts.redisAddr})
		if err != nil {
			return nil, nil, err
		}
		return s, s.Close, nil
	case "postgresql":
		s, err := postgresql.New(ctx, &postgresql.Config{DSN: opts.postgresDSN})
		if err != nil {
			return nil, nil, err
		}
		return s, s.Close, nil
	case "opensearch":
		s, err := opensearch.New(ctx, &opensearch.Config{
			Config: osclient.Config{URLs: []string{opts.opensearchURL}},
			Index:  "approval-records",
		})
		return s, func() error { return nil }, err
	default:
		return nil, nil, errors.Errorf("unknown store backend %q", opts.storeType)
	}
}

func parseCredentials(values []string) ([]approvalserver.ServerCredential, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one -server-credential is required")
	}
	out := make([]approvalserver.ServerCredential, 0, len(values))
	for _, value := range values {
		name, rest, ok := strings.Cut(value, "=")
		if !ok || name == "" || rest == "" {
			return nil, errors.Errorf("invalid server credential %q: expected name=token", value)
		}
		token := rest
		var instances []string
		if tokenPart, instancePart, ok := strings.Cut(rest, "@"); ok {
			token = tokenPart
			for _, instance := range strings.Split(instancePart, ",") {
				if instance = strings.TrimSpace(instance); instance != "" {
					instances = append(instances, instance)
				}
			}
		}
		var hash string
		if !strings.HasPrefix(token, "sha256:") {
			hash = approvalserver.HashServerToken(token)
		} else {
			hash = strings.TrimPrefix(token, "sha256:")
		}
		out = append(out, approvalserver.ServerCredential{
			ServerName: name, TokenHash: hash, AllowedInstances: instances,
		})
	}
	return out, nil
}

func loadPolicy(path string) (*approvalserver.PolicyConfig, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read policy file %q", path)
	}
	var policy approvalserver.PolicyConfig
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, errors.Wrapf(err, "failed to decode policy file %q", path)
	}
	return &policy, nil
}

func defaultBaseURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
