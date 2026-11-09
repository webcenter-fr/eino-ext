package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

const (
	grafanaToken = "grafana-token"
	k8sToken     = "k8s-token"
	receiptKey   = "receipt-secret"
)

func newTestServer(t *testing.T, policy *PolicyConfig) (*Server, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemory()
	s, err := New(context.Background(), &Config{
		BaseURL: "https://approve.example",
		Store:   st,
		ServerCredentials: []ServerCredential{
			{ServerName: "grafana", TokenHash: HashServerToken(grafanaToken), AllowedInstances: []string{"prod"}},
			{ServerName: "kubernetes", TokenHash: HashServerToken(k8sToken)},
		},
		Receipt: ReceiptConfig{Secret: receiptKey},
		Policy:  policy,
	})
	require.NoError(t, err)
	return s, st
}

func registerBody(serverName, toolName, instance string, args json.RawMessage) map[string]any {
	hash, _ := mcpapproval.ArgsHash(args)
	return map[string]any{
		"serverName": serverName,
		"toolName":   toolName,
		"instance":   instance,
		"args":       args,
		"argsHash":   hash,
		"preview":    "preview",
		"requester":  map[string]any{"sub": "alice", "user": "alice", "groups": []string{"devs"}},
	}
}

func doJSON(t *testing.T, s *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func doSession(t *testing.T, s *Server, method, path string, body any, session *Session, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if session != nil {
		req.AddCookie(&http.Cookie{Name: s.cfg.CookieName, Value: session.ID})
		req.AddCookie(&http.Cookie{Name: DefaultCSRFCookie, Value: session.CSRF})
		if csrf {
			req.Header.Set(s.cfg.CSRFHeader, session.CSRF)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func registerApproval(t *testing.T, s *Server, token string, body map[string]any) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", token, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp registerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.ApprovalID)
	return resp.ApprovalID
}

func TestRegisterServerNameBinding(t *testing.T) {
	s, _ := newTestServer(t, nil)
	body := registerBody("kubernetes", "kubernetes_resource_delete", "prod", json.RawMessage(`{"cluster":"prod"}`))
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", grafanaToken, body)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRegisterInstanceBinding(t *testing.T) {
	s, _ := newTestServer(t, nil)
	body := registerBody("grafana", "grafana_dashboard_write", "dev", json.RawMessage(`{"instance":"dev"}`))
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", grafanaToken, body)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRegisterHashMismatch(t *testing.T) {
	s, _ := newTestServer(t, nil)
	body := registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{"a":1}`))
	body["argsHash"] = strings.Repeat("0", 64)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", grafanaToken, body)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterRejectsExcessiveTTL(t *testing.T) {
	s, _ := newTestServer(t, nil)
	body := registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`))
	body["ttl"] = "1000000h" // far above maxApprovalTTL
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", grafanaToken, body)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterRejectsOversizedBody(t *testing.T) {
	s, _ := newTestServer(t, nil)
	body := map[string]any{
		"serverName": "grafana",
		"toolName":   "grafana_dashboard_write",
		"instance":   "prod",
		"args":       map[string]any{"data": strings.Repeat("x", maxRequestBodyBytes)},
		"argsHash":   strings.Repeat("0", 64),
		"requester":  map[string]any{"sub": "alice"},
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/approvals", grafanaToken, body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestRegisterApproveAndReceipt(t *testing.T) {
	s, _ := newTestServer(t, nil)
	args := json.RawMessage(`{"instance":"prod","dashboard":"{}"}`)
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", args))

	session, err := s.sessions.create("alice", "alice", []string{"devs"})
	require.NoError(t, err)

	detail := doSession(t, s, http.MethodGet, "/api/v1/approvals/"+id, nil, session, false)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())

	decide := doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, session, true)
	require.Equal(t, http.StatusOK, decide.Code, decide.Body.String())

	decision := doJSON(t, s, http.MethodGet, "/api/v1/approvals/"+id+"/decision", grafanaToken, nil)
	require.Equal(t, http.StatusOK, decision.Code, decision.Body.String())
	var resp decisionResponse
	require.NoError(t, json.Unmarshal(decision.Body.Bytes(), &resp))
	assert.Equal(t, string(store.StateApproved), resp.State)
	require.NotEmpty(t, resp.Receipt)

	signer, err := mcpapproval.NewReceiptSigner(mcpapproval.ReceiptConfig{Secret: []byte(receiptKey)})
	require.NoError(t, err)
	claims, err := signer.Verify(resp.Receipt)
	require.NoError(t, err)
	assert.Equal(t, id, claims.ApprovalID)
	assert.Equal(t, "grafana", claims.ServerName)
	assert.Equal(t, "grafana_dashboard_write", claims.ToolName)
	assert.Equal(t, "prod", claims.Instance)
	assert.Equal(t, "alice", claims.RequesterSub)
	assert.Equal(t, "alice", claims.ApproverSub)
	assert.Equal(t, "approve", claims.Decision)
	assert.NotEmpty(t, claims.ID)
}

func TestDecideWithoutSession(t *testing.T) {
	s, _ := newTestServer(t, nil)
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))
	rec := doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, nil, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDecideWithoutCSRF(t *testing.T) {
	s, _ := newTestServer(t, nil)
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))
	session, err := s.sessions.create("alice", "alice", nil)
	require.NoError(t, err)
	rec := doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, session, false)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAntiPhishingDifferentUser(t *testing.T) {
	s, _ := newTestServer(t, nil)
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))
	bob, err := s.sessions.create("bob", "bob", nil)
	require.NoError(t, err)
	rec := doSession(t, s, http.MethodGet, "/api/v1/approvals/"+id, nil, bob, false)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestFourEyesPolicy(t *testing.T) {
	s, _ := newTestServer(t, &PolicyConfig{Rules: []PolicyRule{{
		Name: "four-eyes", Tools: []string{"grafana_dashboard_write"}, RequireFourEyes: true,
	}}})
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))

	alice, err := s.sessions.create("alice", "alice", nil)
	require.NoError(t, err)
	rec := doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, alice, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	bob, err := s.sessions.create("bob", "bob", nil)
	require.NoError(t, err)
	rec = doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, bob, true)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestApproverGroupPolicy(t *testing.T) {
	s, _ := newTestServer(t, &PolicyConfig{Rules: []PolicyRule{{
		Name: "sre-only", Tools: []string{"grafana_dashboard_write"}, ApproverGroups: []string{"sre"},
	}}})
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))

	bob, err := s.sessions.create("bob", "bob", []string{"devs"})
	require.NoError(t, err)
	rec := doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, bob, true)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	carol, err := s.sessions.create("carol", "carol", []string{"sre"})
	require.NoError(t, err)
	rec = doSession(t, s, http.MethodPost, "/api/v1/approvals/"+id+"/decide", map[string]string{"decision": "approve"}, carol, true)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestInboxFiltering(t *testing.T) {
	s, _ := newTestServer(t, nil)
	registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))

	alice, err := s.sessions.create("alice", "alice", nil)
	require.NoError(t, err)
	rec := doSession(t, s, http.MethodGet, "/api/v1/approvals", nil, alice, false)
	require.Equal(t, http.StatusOK, rec.Code)
	var items []inboxItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	assert.Len(t, items, 1)

	bob, err := s.sessions.create("bob", "bob", nil)
	require.NoError(t, err)
	rec = doSession(t, s, http.MethodGet, "/api/v1/approvals", nil, bob, false)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	assert.Empty(t, items)
}

func TestDecisionWrongServer(t *testing.T) {
	s, _ := newTestServer(t, nil)
	id := registerApproval(t, s, grafanaToken, registerBody("grafana", "grafana_dashboard_write", "prod", json.RawMessage(`{}`)))
	rec := doJSON(t, s, http.MethodGet, "/api/v1/approvals/"+id+"/decision", k8sToken, nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDecisionExpired(t *testing.T) {
	s, st := newTestServer(t, nil)
	rec := &store.Record{
		ServerName: "grafana", ToolName: "grafana_dashboard_write", Instance: "prod",
		Args: json.RawMessage(`{}`), ArgsHash: "hash", Requester: store.Requester{Sub: "alice"},
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	require.NoError(t, st.Create(context.Background(), rec))
	resp := doJSON(t, s, http.MethodGet, "/api/v1/approvals/"+rec.ID+"/decision", grafanaToken, nil)
	require.Equal(t, http.StatusOK, resp.Code)
	var decision decisionResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &decision))
	assert.Equal(t, string(store.StateExpired), decision.State)
}

func TestSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doJSON(t, s, http.MethodGet, "/healthz", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "max-age=31536000", rec.Header().Get("Strict-Transport-Security"))
	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.NotContains(t, csp, "unsafe-inline")
	assert.NotContains(t, csp, "unsafe-eval")
}

func TestSPAFallbackAndNonce(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doJSON(t, s, http.MethodGet, "/approvals/unknown", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "nonce=")
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "nonce-")
}

func TestLogoutRequiresCSRF(t *testing.T) {
	s, _ := newTestServer(t, nil)
	session, err := s.sessions.create("alice", "alice", nil)
	require.NoError(t, err)

	rec := doSession(t, s, http.MethodGet, "/auth/logout", nil, session, false)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = doSession(t, s, http.MethodGet, "/auth/logout?csrf="+session.CSRF, nil, session, false)
	assert.Equal(t, http.StatusFound, rec.Code)
}

func TestLoginWithoutOIDC(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doJSON(t, s, http.MethodGet, "/auth/login", "", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
