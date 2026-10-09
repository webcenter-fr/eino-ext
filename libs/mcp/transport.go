package mcp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeStdio serves the MCP server over stdio (single session, local identity).
// It blocks until ctx is cancelled or the session ends.
func (s *Server) ServeStdio(ctx context.Context) error {
	srv, err := s.ServerFor(ctx, s.cfg.LocalIdentity)
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcpsdk.StdioTransport{})
}

// HTTPHandler returns an http.Handler serving the streamable HTTP transport.
// When auth providers are configured, the handler is wrapped with
// auth.RequireBearerToken (401 + WWW-Authenticate on failure).
//
// The transport is stateful (the SDK default): stateless mode rejects
// server→client requests, which would break elicitation-based approval.
func (s *Server) HTTPHandler() http.Handler {
	getServer := func(r *http.Request) *mcpsdk.Server {
		id := identityFromTokenInfo(auth.TokenInfoFromContext(r.Context()))
		srv, err := s.ServerFor(r.Context(), id)
		if err != nil {
			return nil // SDK responds 400 "no server available"
		}
		return srv
	}
	handler := mcpsdk.NewStreamableHTTPHandler(getServer, &mcpsdk.StreamableHTTPOptions{})
	if len(s.providers) > 0 {
		var opts *auth.RequireBearerTokenOptions
		if s.cfg.Auth != nil && (s.cfg.Auth.ResourceMetadataURL != "" || len(s.cfg.Auth.RequiredScopes) > 0) {
			opts = &auth.RequireBearerTokenOptions{
				ResourceMetadataURL: s.cfg.Auth.ResourceMetadataURL,
				Scopes:              s.cfg.Auth.RequiredScopes,
			}
		}
		return auth.RequireBearerToken(tokenVerifier(s.providers), opts)(handler)
	}
	return handler
}

// ListenAndServeHTTP serves the HTTP handler on addr with graceful shutdown on
// ctx.Done().
func (s *Server) ListenAndServeHTTP(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.HTTPHandler()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
