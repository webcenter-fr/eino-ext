package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Session is a server-side browser session.
type Session struct {
	ID        string
	Sub       string
	User      string
	Groups    []string
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	now      func() time.Time
}

func newSessionStore(ttl time.Duration, now func() time.Time) *sessionStore {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	if now == nil {
		now = time.Now
	}
	return &sessionStore{sessions: make(map[string]*Session), ttl: ttl, now: now}
}

func (s *sessionStore) create(sub, user string, groups []string) (*Session, error) {
	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	session := &Session{
		ID:        id,
		Sub:       sub,
		User:      user,
		Groups:    append([]string(nil), groups...),
		CSRF:      csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()
	return session, nil
}

func (s *sessionStore) get(id string) *Session {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil
	}
	if !s.now().Before(session.ExpiresAt) {
		delete(s.sessions, id)
		return nil
	}
	out := *session
	out.Groups = append([]string(nil), session.Groups...)
	return &out
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *sessionStore) rotate(oldID, sub, user string, groups []string) (*Session, error) {
	s.delete(oldID)
	return s.create(sub, user, groups)
}

type oauthState struct {
	Verifier  string
	Nonce     string
	ExpiresAt time.Time
}

type oauthStateStore struct {
	mu     sync.Mutex
	states map[string]oauthState
	now    func() time.Time
}

func newOAuthStateStore(now func() time.Time) *oauthStateStore {
	if now == nil {
		now = time.Now
	}
	return &oauthStateStore{states: make(map[string]oauthState), now: now}
}

func (s *oauthStateStore) create(state, verifier, nonce string) {
	s.mu.Lock()
	s.states[state] = oauthState{Verifier: verifier, Nonce: nonce, ExpiresAt: s.now().Add(10 * time.Minute)}
	s.mu.Unlock()
}

func (s *oauthStateStore) consume(state string) (oauthState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.states[state]
	if !ok {
		return oauthState{}, false
	}
	delete(s.states, state)
	if !s.now().Before(value.ExpiresAt) {
		return oauthState{}, false
	}
	return value, true
}

type oidcProvider struct {
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       oauth2.Config
	userClaim   string
	groupsClaim string
}

func newOIDCProvider(ctx context.Context, cfg *OIDCConfig) (*oidcProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch OIDC discovery document")
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = strings.Fields(DefaultOIDCScopes)
	}
	userClaim := cfg.UserClaim
	if userClaim == "" {
		userClaim = "preferred_username"
	}
	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	return &oidcProvider{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		userClaim:   userClaim,
		groupsClaim: groupsClaim,
	}, nil
}

func (p *oidcProvider) authCodeURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func (p *oidcProvider) exchange(ctx context.Context, code, verifier string) (*oidc.IDToken, map[string]any, error) {
	token, err := p.oauth.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return nil, nil, errors.Wrap(err, "OIDC code exchange failed")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, nil, errors.New("OIDC token response has no id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, errors.Wrap(err, "OIDC id_token verification failed")
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, nil, errors.Wrap(err, "failed to read OIDC claims")
	}
	return idToken, claims, nil
}

func (p *oidcProvider) identity(idToken *oidc.IDToken, claims map[string]any) (sub, user string, groups []string) {
	sub = idToken.Subject
	user = stringClaim(claims, p.userClaim)
	if user == "" {
		user = sub
	}
	groups = stringSliceClaim(claims, p.groupsClaim)
	return sub, user, groups
}

func stringClaim(claims map[string]any, name string) string {
	s, _ := claims[name].(string)
	return s
}

func stringSliceClaim(claims map[string]any, name string) []string {
	raw, ok := claims[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "failed to generate random token")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashServerToken returns the lowercase hex SHA-256 of a server token. It is
// the value stored in ServerCredential.TokenHash.
func HashServerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) matchCredential(token string) *ServerCredential {
	if token == "" {
		return nil
	}
	presented := sha256.Sum256([]byte(token))
	var match *ServerCredential
	for i := range s.cfg.ServerCredentials {
		expected, err := hex.DecodeString(s.cfg.ServerCredentials[i].TokenHash)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(expected, presented[:]) == 1 {
			match = &s.cfg.ServerCredentials[i]
		}
	}
	return match
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

type credentialContextKey struct{}

func credentialFromContext(ctx context.Context) *ServerCredential {
	cred, _ := ctx.Value(credentialContextKey{}).(*ServerCredential)
	return cred
}

func (s *Server) requireServerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred := s.matchCredential(bearerToken(r))
		if cred == nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing server token")
			return
		}
		ctx := context.WithValue(r.Context(), credentialContextKey{}, cred)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) sessionFromRequest(r *http.Request) *Session {
	cookie, err := r.Cookie(s.cfg.CookieName)
	if err != nil {
		return nil
	}
	return s.sessions.get(cookie.Value)
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := s.sessionFromRequest(r)
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type sessionContextKey struct{}

func sessionFromContext(ctx context.Context) *Session {
	session, _ := ctx.Value(sessionContextKey{}).(*Session)
	return session
}

func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := sessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		cookie, err := r.Cookie(DefaultCSRFCookie)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, "missing CSRF cookie")
			return
		}
		header := r.Header.Get(s.cfg.CSRFHeader)
		if header == "" {
			header = r.FormValue("csrf")
		}
		if !constantTimeEqual(session.CSRF, cookie.Value) || !constantTimeEqual(session.CSRF, header) {
			writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) setSessionCookie(w http.ResponseWriter, session *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     DefaultCSRFCookie,
		Value:    session.CSRF,
		Path:     "/",
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{s.cfg.CookieName, DefaultCSRFCookie, DefaultOAuthCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name != DefaultCSRFCookie,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}
