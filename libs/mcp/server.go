// Package mcp exposes eino tools as an MCP server with provider-based
// authentication, RBAC, and elicitation-based human approval for write tools.
package mcp

import (
	"context"
	"sync"
	"time"

	emperrors "emperror.dev/errors"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Server exposes eino tools over MCP with auth, RBAC, and elicitation-based
// human approval for write tools.
type Server struct {
	cfg             *Config
	authz           *Authorizer
	providers       []Provider
	writeTools      map[string]bool
	approvalTimeout time.Duration
	impl            *mcpsdk.Implementation

	mu      sync.Mutex
	servers map[string]*mcpsdk.Server // per-identity cache (single entry when authz == nil)
}

// NewServer builds a Server from the config. It validates the config (after
// applying defaults), builds the RBAC authorizer and the auth providers.
func NewServer(ctx context.Context, cfg *Config) (*Server, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	// Defaults (before validation, per repo convention).
	if cfg.Name == "" {
		cfg.Name = "eino-ext-mcp"
	}
	if cfg.Version == "" {
		cfg.Version = "0.0.0"
	}
	if cfg.AuditSink == nil {
		cfg.AuditSink = &safety.LogSink{}
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	s := &Server{
		cfg:        cfg,
		writeTools: safety.NewWriteToolSet(cfg.WriteTools),
		servers:    make(map[string]*mcpsdk.Server),
		impl:       &mcpsdk.Implementation{Name: cfg.Name, Version: cfg.Version},
	}

	s.approvalTimeout = 5 * time.Minute
	if cfg.Approval != nil && cfg.Approval.Timeout != "" {
		d, err := time.ParseDuration(cfg.Approval.Timeout)
		if err != nil {
			return nil, emperrors.Wrap(err, "invalid approval timeout")
		}
		s.approvalTimeout = d
	}

	if cfg.Authorization != nil {
		authz, err := NewAuthorizer(cfg.Authorization)
		if err != nil {
			return nil, err
		}
		s.authz = authz
	}

	if cfg.Auth != nil {
		providers, err := newProviders(ctx, cfg.Auth)
		if err != nil {
			return nil, err
		}
		s.providers = providers
	}

	if s.authz != nil && len(s.providers) == 0 {
		logrus.Warn("mcp: authorization configured without auth providers; unauthenticated HTTP calls will be denied (fail closed)")
	}

	return s, nil
}

// ServerFor returns the per-identity *mcpsdk.Server (building and caching it on
// first use). When no RBAC is configured, a single shared server with all tools
// is returned. Exported for tests and custom transports.
func (s *Server) ServerFor(ctx context.Context, id *Identity) (*mcpsdk.Server, error) {
	// Without RBAC every identity shares one server (all tools); with RBAC one
	// server per identity, cached by identity key.
	key := identityCacheKey(id)
	if s.authz == nil {
		key = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[key]; ok {
		return srv, nil
	}
	srv := mcpsdk.NewServer(s.impl, nil)
	if err := s.addTools(ctx, srv, id); err != nil {
		return nil, err
	}
	s.servers[key] = srv
	return srv, nil
}

// identityFromRequest resolves the caller identity: the per-request bearer token
// info (HTTP) or the configured local identity (stdio / no auth).
func (s *Server) identityFromRequest(req *mcpsdk.CallToolRequest) *Identity {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		return identityFromTokenInfo(req.Extra.TokenInfo)
	}
	return s.cfg.LocalIdentity
}

// audit sends an audit event to the configured sink (best-effort).
func (s *Server) audit(ctx context.Context, event safety.AuditEvent) {
	_ = s.cfg.AuditSink.Write(ctx, event)
}
