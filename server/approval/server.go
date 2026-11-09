package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// maxRequestBodyBytes bounds the JSON request bodies accepted by the register
// and decide endpoints. It is a DoS guard, not an argument-size limit: tool
// arguments are far smaller than this, and the approval path itself imposes no
// size cap (see the plan's "remove the 4000-byte args limit" decision).
const maxRequestBodyBytes = 16 << 20 // 16 MiB

// maxApprovalTTL caps the record lifetime a client may request. The server
// default (15m) already exceeds the MCP timeout (5m); an unbounded client TTL
// would let a compromised or buggy MCP server pin records (and memory) forever.
const maxApprovalTTL = time.Hour

// Server is the standalone approval service.
type Server struct {
	cfg         *Config
	store       store.ApprovalStore
	policy      *ApprovalPolicy
	sessions    *sessionStore
	oauthStates *oauthStateStore
	oidc        *oidcProvider
	receipt     *mcpapproval.ReceiptSigner
	ttl         time.Duration
	now         func() time.Time
	mux         *http.ServeMux
}

// New builds the approval service. It applies defaults, validates the config,
// builds the receipt signer and (when configured) fetches OIDC discovery.
func New(ctx context.Context, cfg *Config) (*Server, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.TTL == "" {
		cfg.TTL = DefaultTTL.String()
	}
	if cfg.SessionTTL == "" {
		cfg.SessionTTL = DefaultSessionTTL.String()
	}
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultCookieName
	}
	if cfg.CSRFHeader == "" {
		cfg.CSRFHeader = DefaultCSRFHeader
	}
	if cfg.Receipt.Algorithm == "" {
		cfg.Receipt.Algorithm = DefaultReceiptAlg
	}
	if cfg.Receipt.TTL == "" {
		cfg.Receipt.TTL = DefaultReceiptTTL.String()
	}
	if cfg.Store == nil {
		cfg.Store = store.NewMemory()
	}
	if cfg.Notifier == nil {
		cfg.Notifier = NoopNotifier{}
	}
	if cfg.AuditSink == nil {
		cfg.AuditSink = &LogAuditSink{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	ttl, err := time.ParseDuration(cfg.TTL)
	if err != nil || ttl <= 0 {
		return nil, errors.Errorf("invalid approval TTL %q: must be a positive Go duration", cfg.TTL)
	}
	sessionTTL, err := time.ParseDuration(cfg.SessionTTL)
	if err != nil || sessionTTL <= 0 {
		return nil, errors.Errorf("invalid session TTL %q: must be a positive Go duration", cfg.SessionTTL)
	}
	receiptTTL, err := time.ParseDuration(cfg.Receipt.TTL)
	if err != nil || receiptTTL <= 0 {
		return nil, errors.Errorf("invalid receipt TTL %q: must be a positive Go duration", cfg.Receipt.TTL)
	}
	receipt, err := mcpapproval.NewReceiptSigner(mcpapproval.ReceiptConfig{
		Secret:        []byte(cfg.Receipt.Secret),
		PrivateKeyPEM: []byte(cfg.Receipt.PrivateKeyPEM),
		KeyID:         cfg.Receipt.KeyID,
		Algorithm:     cfg.Receipt.Algorithm,
		TTL:           receiptTTL,
	})
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:         cfg,
		store:       cfg.Store,
		policy:      NewApprovalPolicy(cfg.Policy),
		sessions:    newSessionStore(sessionTTL, cfg.Now),
		oauthStates: newOAuthStateStore(cfg.Now),
		receipt:     receipt,
		ttl:         ttl,
		now:         cfg.Now,
		mux:         http.NewServeMux(),
	}
	if cfg.OIDC != nil {
		oidcProvider, err := newOIDCProvider(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		s.oidc = oidcProvider
	}
	s.routes()
	return s, nil
}

// Handler returns the HTTP handler with security headers.
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.mux)
}

func (s *Server) routes() {
	s.mux.Handle("POST /api/v1/approvals", s.requireServerToken(http.HandlerFunc(s.handleRegister)))
	s.mux.Handle("GET /api/v1/approvals/{id}/decision", s.requireServerToken(http.HandlerFunc(s.handleDecision)))
	s.mux.Handle("GET /api/v1/approvals", s.requireSession(http.HandlerFunc(s.handleInbox)))
	s.mux.Handle("GET /api/v1/approvals/{id}", s.requireSession(http.HandlerFunc(s.handleDetail)))
	s.mux.Handle("POST /api/v1/approvals/{id}/decide", s.requireSession(s.requireCSRF(http.HandlerFunc(s.handleDecide))))
	s.mux.HandleFunc("GET /auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /auth/callback", s.handleCallback)
	s.mux.HandleFunc("GET /auth/logout", s.handleLogout)
	s.mux.HandleFunc("POST /auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.Handle("/", s.spaHandler())
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; "+
				"frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// The REST DTOs are shared with the client package so the wire contract stays
// in sync in one place.
type (
	registerRequest  = mcpapproval.RegisterRequest
	registerResponse = mcpapproval.RegisterResponse
	decisionResponse = mcpapproval.Decision
)

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	cred := credentialFromContext(r.Context())
	var req registerRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.ServerName == "" || req.ToolName == "" || req.ArgsHash == "" || req.Requester.Sub == "" {
		writeJSONError(w, http.StatusBadRequest, "serverName, toolName, argsHash and requester.sub are required")
		return
	}
	if req.ServerName != cred.ServerName {
		writeJSONError(w, http.StatusForbidden, "serverName does not match the credential binding")
		return
	}
	if !instanceAllowed(cred.AllowedInstances, req.Instance) {
		writeJSONError(w, http.StatusForbidden, "instance is not allowed for this credential")
		return
	}
	hash, err := mcpapproval.ArgsHash(req.Args)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid args: "+err.Error())
		return
	}
	if hash != req.ArgsHash {
		writeJSONError(w, http.StatusBadRequest, "argsHash does not match the submitted args")
		return
	}
	ttl := s.ttl
	if req.TTL != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid ttl")
			return
		}
		if parsed > maxApprovalTTL {
			writeJSONError(w, http.StatusBadRequest, "ttl exceeds the maximum allowed")
			return
		}
		ttl = parsed
	}
	now := s.now()
	rec := &store.Record{
		ServerName:     req.ServerName,
		ToolName:       req.ToolName,
		Instance:       req.Instance,
		Operation:      req.Operation,
		Args:           req.Args,
		ArgsHash:       req.ArgsHash,
		Preview:        req.Preview,
		PreviewWarning: req.PreviewWarning,
		Requester: store.Requester{
			Sub:    req.Requester.Sub,
			User:   req.Requester.User,
			Groups: req.Requester.Groups,
		},
		State:     store.StatePending,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.store.Create(r.Context(), rec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to store approval")
		return
	}
	s.audit(r.Context(), AuditEvent{
		Timestamp: s.now(), ApprovalID: rec.ID, ServerName: rec.ServerName,
		ToolName: rec.ToolName, Instance: rec.Instance, RequesterSub: rec.Requester.Sub,
		ArgsHash: rec.ArgsHash, State: string(rec.State),
	})
	_ = s.cfg.Notifier.Notify(r.Context(), rec)
	writeJSON(w, http.StatusCreated, registerResponse{
		ApprovalID: rec.ID,
		URL:        strings.TrimRight(s.cfg.BaseURL, "/") + "/approvals/" + rec.ID,
		ExpiresAt:  rec.ExpiresAt,
	})
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request) {
	cred := credentialFromContext(r.Context())
	id := r.PathValue("id")
	wait := time.Duration(0)
	if raw := r.URL.Query().Get("wait"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid wait")
			return
		}
		wait = parsed
		if wait > mcpapproval.MaxPollSlice {
			wait = mcpapproval.MaxPollSlice
		}
	}
	rec, err := s.store.WaitDecision(r.Context(), id, wait)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "approval not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to read approval")
		return
	}
	if rec.ServerName != cred.ServerName {
		writeJSONError(w, http.StatusForbidden, "approval belongs to another server")
		return
	}
	resp := decisionResponse{
		State:        string(rec.State),
		ApprovalID:   rec.ID,
		ServerName:   rec.ServerName,
		ToolName:     rec.ToolName,
		Instance:     rec.Instance,
		ArgsHash:     rec.ArgsHash,
		RequesterSub: rec.Requester.Sub,
		ApproverSub:  rec.ApproverSub,
		Decision:     rec.Decision,
		Policy:       rec.Policy,
		ExpiresAt:    rec.ExpiresAt,
	}
	if rec.State == store.StateApproved {
		token, claims, err := s.receipt.Issue(mcpapproval.ReceiptClaims{
			ApprovalID:   rec.ID,
			ServerName:   rec.ServerName,
			ToolName:     rec.ToolName,
			Instance:     rec.Instance,
			RequesterSub: rec.Requester.Sub,
			ApproverSub:  rec.ApproverSub,
			ArgsHash:     rec.ArgsHash,
			Decision:     rec.Decision,
			Policy:       rec.Policy,
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to issue receipt")
			return
		}
		resp.Receipt = token
		s.audit(r.Context(), AuditEvent{
			Timestamp: s.now(), ApprovalID: rec.ID, ServerName: rec.ServerName,
			ToolName: rec.ToolName, Instance: rec.Instance, RequesterSub: rec.Requester.Sub,
			ApproverSub: rec.ApproverSub, Decision: rec.Decision, Policy: rec.Policy,
			ArgsHash: rec.ArgsHash, State: string(rec.State), JTI: claims.ID,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type inboxItem struct {
	ApprovalID   string    `json:"approvalId"`
	ServerName   string    `json:"serverName"`
	ToolName     string    `json:"toolName"`
	Instance     string    `json:"instance,omitempty"`
	RequesterSub string    `json:"requesterSub"`
	Requester    string    `json:"requester,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	State        string    `json:"state"`
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	session := sessionFromContext(r.Context())
	records, err := s.store.ListPending(r.Context(), store.ListFilter{})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list approvals")
		return
	}
	items := make([]inboxItem, 0, len(records))
	for _, rec := range records {
		if !s.policy.CanDecide(rec, session) {
			continue
		}
		items = append(items, inboxItem{
			ApprovalID: rec.ID, ServerName: rec.ServerName, ToolName: rec.ToolName,
			Instance: rec.Instance, RequesterSub: rec.Requester.Sub, Requester: rec.Requester.User,
			CreatedAt: rec.CreatedAt, ExpiresAt: rec.ExpiresAt, State: string(rec.State),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	session := sessionFromContext(r.Context())
	rec, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "approval not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to read approval")
		return
	}
	if _, err := s.policy.Evaluate(rec, session); err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

type decideRequest struct {
	Decision string `json:"decision"`
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	session := sessionFromContext(r.Context())
	rec, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "approval not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to read approval")
		return
	}
	policyName, err := s.policy.Evaluate(rec, session)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}
	var req decideRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		return
	}
	if req.Decision != store.DecisionApprove && req.Decision != store.DecisionReject {
		writeJSONError(w, http.StatusBadRequest, "decision must be 'approve' or 'reject'")
		return
	}
	decided, err := s.store.Decide(r.Context(), rec.ID, store.Decision{
		Decision: req.Decision, ApproverSub: session.Sub, ApproverUser: session.User, Policy: policyName,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAlreadyDecided):
			writeJSONError(w, http.StatusConflict, "approval already decided")
		case errors.Is(err, store.ErrExpired):
			writeJSONError(w, http.StatusConflict, "approval expired")
		default:
			writeJSONError(w, http.StatusInternalServerError, "failed to record decision")
		}
		return
	}
	s.audit(r.Context(), AuditEvent{
		Timestamp: s.now(), ApprovalID: decided.ID, ServerName: decided.ServerName,
		ToolName: decided.ToolName, Instance: decided.Instance, RequesterSub: decided.Requester.Sub,
		ApproverSub: decided.ApproverSub, Decision: decided.Decision, Policy: decided.Policy,
		ArgsHash: decided.ArgsHash, State: string(decided.State),
	})
	writeJSON(w, http.StatusOK, decisionResponse{
		State: string(decided.State), ApprovalID: decided.ID, ServerName: decided.ServerName,
		ToolName: decided.ToolName, Instance: decided.Instance, ArgsHash: decided.ArgsHash,
		RequesterSub: decided.Requester.Sub, ApproverSub: decided.ApproverSub,
		Decision: decided.Decision, Policy: decided.Policy, ExpiresAt: decided.ExpiresAt,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "OIDC is not configured")
		return
	}
	state, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to start login")
		return
	}
	verifier, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to start login")
		return
	}
	nonce, err := randomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to start login")
		return
	}
	s.oauthStates.create(state, verifier, nonce)
	http.SetCookie(w, &http.Cookie{
		Name: DefaultOAuthCookie, Value: state, Path: "/", HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	http.Redirect(w, r, s.oidc.authCodeURL(state, nonce, verifier), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "OIDC is not configured")
		return
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(DefaultOAuthCookie)
	if err != nil || !constantTimeEqual(cookie.Value, state) {
		writeJSONError(w, http.StatusBadRequest, "invalid OAuth state")
		return
	}
	pending, ok := s.oauthStates.consume(state)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "expired OAuth state")
		return
	}
	idToken, claims, err := s.oidc.exchange(r.Context(), r.URL.Query().Get("code"), pending.Verifier)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if idToken.Nonce != pending.Nonce {
		writeJSONError(w, http.StatusUnauthorized, "OIDC nonce mismatch")
		return
	}
	sub, user, groups := s.oidc.identity(idToken, claims)
	oldID := ""
	if existing, err := r.Cookie(s.cfg.CookieName); err == nil {
		oldID = existing.Value
	}
	session, err := s.sessions.rotate(oldID, sub, user, groups)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	s.setSessionCookie(w, session)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session := s.sessionFromRequest(r)
	if session == nil {
		s.clearSessionCookies(w)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	csrf := r.Header.Get(s.cfg.CSRFHeader)
	if csrf == "" {
		csrf = r.URL.Query().Get("csrf")
	}
	cookie, err := r.Cookie(DefaultCSRFCookie)
	if err != nil || !constantTimeEqual(session.CSRF, csrf) || !constantTimeEqual(session.CSRF, cookie.Value) {
		writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	s.sessions.delete(session.ID)
	s.clearSessionCookies(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) audit(ctx context.Context, event AuditEvent) {
	_ = s.cfg.AuditSink.Write(ctx, event)
}

func instanceAllowed(allowed []string, instance string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == "*" || candidate == instance {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// decodeJSONBody decodes a JSON request body bounded by maxRequestBodyBytes.
// It writes the error response itself and returns a non-nil error on failure.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return err
		}
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return err
	}
	return nil
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
