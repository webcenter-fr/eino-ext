package mcp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/golang-jwt/jwt/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerForCachesPerIdentity(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools:         []tool.InvokableTool{&fakeTool{name: "read_tool"}},
		Authorization: &AuthzConfig{Rules: []AccessRule{{Users: []string{"alice"}}}},
	})
	ctx := context.Background()

	srv1, err := s.ServerFor(ctx, &Identity{User: "alice", Groups: []string{"dev", "ops"}})
	require.NoError(t, err)
	// Same user+groups (any order) → same cached server.
	srv2, err := s.ServerFor(ctx, &Identity{User: "alice", Groups: []string{"ops", "dev"}})
	require.NoError(t, err)
	assert.Same(t, srv1, srv2)

	srv3, err := s.ServerFor(ctx, &Identity{User: "bob"})
	require.NoError(t, err)
	assert.NotSame(t, srv1, srv3)
}

func TestServerForDefaultServerCached(t *testing.T) {
	// Without RBAC a single shared server with all tools is returned.
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{&fakeTool{name: "read_tool"}},
	})
	ctx := context.Background()

	srv1, err := s.ServerFor(ctx, nil)
	require.NoError(t, err)
	srv2, err := s.ServerFor(ctx, &Identity{User: "anyone"})
	require.NoError(t, err)
	assert.Same(t, srv1, srv2)
}

func TestToolsListFilteredByIdentity(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{
			&fakeTool{name: "read_tool"},
			&fakeTool{name: "write_tool"},
		},
		WriteTools: []string{"write_tool"},
		Authorization: &AuthzConfig{Rules: []AccessRule{
			{Users: []string{"reader"}, Operations: []string{"read"}},
			{Users: []string{"writer"}},
		}},
	})
	ctx := context.Background()

	// Read-only identity sees only read tools.
	csReader := connectInMemory(t, s, &Identity{User: "reader"}, nil)
	listRes, err := csReader.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")
	assert.NotContains(t, toolNames(listRes), "write_tool")

	// Read-write identity sees all tools.
	csWriter := connectInMemory(t, s, &Identity{User: "writer"}, nil)
	listRes, err = csWriter.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")
	assert.Contains(t, toolNames(listRes), "write_tool")
}

func TestHTTPHandlerRejectsMissingOrBadToken(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{&fakeTool{name: "read_tool"}},
		Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
			Tokens: []LocalToken{{Token: "good-token", User: "alice"}},
		}}}},
	})
	ts := httptest.NewServer(s.HTTPHandler())
	// Registered as a cleanup (before connectHTTP) so it runs AFTER the client
	// sessions are closed: httptest.Server.Close blocks on the standalone SSE
	// connections otherwise.
	t.Cleanup(ts.Close)

	// No token → 401 before any tool handler runs.
	resp, err := http.Post(ts.URL, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Bad token → 401.
	req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer bad-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestHTTPHandlerValidLocalToken(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{&fakeTool{name: "read_tool"}},
		Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
			Tokens: []LocalToken{{Token: "good-token", User: "alice"}},
		}}}},
	})
	ts := httptest.NewServer(s.HTTPHandler())
	// Registered as a cleanup (before connectHTTP) so it runs AFTER the client
	// sessions are closed: httptest.Server.Close blocks on the standalone SSE
	// connections otherwise.
	t.Cleanup(ts.Close)

	cs := connectHTTP(t, ts.URL, httpClientWithToken("good-token"), nil)
	listRes, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")

	res := callTool(t, cs, "read_tool", map[string]any{"cluster": "prod"})
	assert.False(t, res.IsError)
}

func TestHTTPHandlerValidOIDCToken(t *testing.T) {
	idp := newTestIDP(t)
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{&fakeTool{name: "read_tool"}},
		Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "oidc", OIDC: &OIDCProviderConfig{
			Issuer:   idp.issuer(),
			Audience: "mcp-server",
		}}}},
	})
	ts := httptest.NewServer(s.HTTPHandler())
	// Registered as a cleanup (before connectHTTP) so it runs AFTER the client
	// sessions are closed: httptest.Server.Close blocks on the standalone SSE
	// connections otherwise.
	t.Cleanup(ts.Close)

	token := idp.sign(t, jwt.MapClaims{"sub": "u", "preferred_username": "alice"})
	cs := connectHTTP(t, ts.URL, httpClientWithToken(token), nil)
	listRes, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")
}

func TestHTTPHandlerPerIdentityToolLists(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{
			&fakeTool{name: "read_tool"},
			&fakeTool{name: "write_tool"},
		},
		WriteTools: []string{"write_tool"},
		Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
			Tokens: []LocalToken{
				{Token: "reader-token", User: "reader"},
				{Token: "writer-token", User: "writer"},
			},
		}}}},
		Authorization: &AuthzConfig{Rules: []AccessRule{
			{Users: []string{"reader"}, Operations: []string{"read"}},
			{Users: []string{"writer"}},
		}},
	})
	ts := httptest.NewServer(s.HTTPHandler())
	// Registered as a cleanup (before connectHTTP) so it runs AFTER the client
	// sessions are closed: httptest.Server.Close blocks on the standalone SSE
	// connections otherwise.
	t.Cleanup(ts.Close)

	csReader := connectHTTP(t, ts.URL, httpClientWithToken("reader-token"), nil)
	listRes, err := csReader.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")
	assert.NotContains(t, toolNames(listRes), "write_tool")

	csWriter := connectHTTP(t, ts.URL, httpClientWithToken("writer-token"), nil)
	listRes, err = csWriter.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	assert.Contains(t, toolNames(listRes), "read_tool")
	assert.Contains(t, toolNames(listRes), "write_tool")
}

func TestHTTPHandlerElicitationApproval(t *testing.T) {
	// Full approval flow over HTTP: the stateful streamable transport carries
	// the server→client elicitation request.
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		Auth: &AuthConfig{Providers: []ProviderConfig{{Type: "local", Local: &LocalProviderConfig{
			Tokens: []LocalToken{{Token: "good-token", User: "alice"}},
		}}}},
		AuditSink: newTestSink(t),
	})
	ts := httptest.NewServer(s.HTTPHandler())
	// Registered as a cleanup (before connectHTTP) so it runs AFTER the client
	// sessions are closed: httptest.Server.Close blocks on the standalone SSE
	// connections otherwise.
	t.Cleanup(ts.Close)

	cs := connectHTTP(t, ts.URL, httpClientWithToken("good-token"), elicitationClient("accept"))
	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "confirmed": true})
	assert.False(t, res.IsError)
	assert.Equal(t, int32(1), ft.calls.Load())
	assert.True(t, ft.authorizedFor.Load())
}

func TestListenAndServeHTTPSmoke(t *testing.T) {
	s := newTestServer(t, &Config{
		Tools: []tool.InvokableTool{&fakeTool{name: "read_tool"}},
	})
	// Grab a free port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServeHTTP(ctx, addr) }()

	// Wait for the server to accept connections.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)

	// The handler serves MCP over HTTP (no auth configured → no 401).
	req, err := http.NewRequest(http.MethodPost, "http://"+addr,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Graceful shutdown on ctx cancellation.
	cancel()
	err = <-errCh
	assert.ErrorIs(t, err, context.Canceled)
}

// Note: ServeStdio is a thin srv.Run(ctx, &mcpsdk.StdioTransport{}) wrapper;
// the in-memory tests above cover the server logic it exposes.
