package mcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/golang-jwt/jwt/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// testClientImpl is the client implementation advertised by test clients.
var testClientImpl = &mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}

// fakeTool is a configurable tool.InvokableTool for tests. It records how many
// times it ran and whether the execution grant was visible in the context.
type fakeTool struct {
	name   string
	desc   string
	params *schema.ParamsOneOf
	run    func(ctx context.Context, args string) (string, error)

	calls         atomic.Int32
	authorizedFor atomic.Bool
}

func (f *fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: f.desc, ParamsOneOf: f.params}, nil
}

func (f *fakeTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	f.calls.Add(1)
	f.authorizedFor.Store(safety.ExecutionAuthorizedFor(ctx, f.name))
	if f.run != nil {
		return f.run(ctx, args)
	}
	return "ok", nil
}

// stubAuthorizer is a safety.ExecutionAuthorizer returning a fixed error.
type stubAuthorizer struct{ err error }

func (s stubAuthorizer) AuthorizeExecute(context.Context, string, json.RawMessage) error {
	return s.err
}

// stubPolicy is a safety.Policy returning a fixed error.
type stubPolicy struct{ err error }

func (s stubPolicy) Evaluate(context.Context, string, map[string]any) error { return s.err }

// newTestServer builds a Server, defaulting InstanceParam to "cluster".
func newTestServer(t *testing.T, cfg *Config) *Server {
	t.Helper()
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.InstanceParam == "" {
		cfg.InstanceParam = "cluster"
	}
	s, err := NewServer(context.Background(), cfg)
	require.NoError(t, err)
	return s
}

// newTestSink returns an audit sink that suppresses log noise; it is closed at
// test cleanup.
func newTestSink(t *testing.T) *safety.ChannelSink {
	t.Helper()
	sink := safety.NewChannelSink(10)
	t.Cleanup(sink.Close)
	return sink
}

// nextAuditEvent returns the next audit event from the sink (fails the test on
// timeout).
func nextAuditEvent(t *testing.T, sink *safety.ChannelSink) safety.AuditEvent {
	t.Helper()
	select {
	case event := <-sink.Events():
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for audit event")
		return safety.AuditEvent{}
	}
}

// connectInMemory builds the per-identity server, connects it to an MCP client
// over in-memory transports, and returns the client session.
func connectInMemory(t *testing.T, s *Server, id *Identity, clientOpts *mcpsdk.ClientOptions) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv, err := s.ServerFor(ctx, id)
	require.NoError(t, err)
	ct, st := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	if clientOpts == nil {
		clientOpts = &mcpsdk.ClientOptions{}
	}
	client := mcpsdk.NewClient(testClientImpl, clientOpts)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool and returns the result (protocol errors fail the test).
func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

// resultText extracts the text of the first content block of a tool result.
func resultText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "expected TextContent, got %T", res.Content[0])
	return tc.Text
}

// toolNames returns the names of the tools in a list result.
func toolNames(res *mcpsdk.ListToolsResult) []string {
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// elicitationClient returns client options whose elicitation handler answers
// with the given action ("accept", "decline", "cancel").
func elicitationClient(action string) *mcpsdk.ClientOptions {
	return &mcpsdk.ClientOptions{
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return &mcpsdk.ElicitResult{Action: action}, nil
		},
	}
}

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// httpClientWithToken returns an HTTP client that injects a bearer token.
func httpClientWithToken(token string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// connectHTTP connects an MCP client to a streamable HTTP endpoint.
func connectHTTP(t *testing.T, url string, httpClient *http.Client, clientOpts *mcpsdk.ClientOptions) *mcpsdk.ClientSession {
	t.Helper()
	if clientOpts == nil {
		clientOpts = &mcpsdk.ClientOptions{}
	}
	client := mcpsdk.NewClient(testClientImpl, clientOpts)
	transport := &mcpsdk.StreamableClientTransport{Endpoint: url, HTTPClient: httpClient}
	cs, err := client.Connect(context.Background(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// testIDP is a minimal in-test OIDC issuer: it serves the discovery document
// and a JWKS endpoint, and signs JWTs with a test RSA key. It counts discovery
// and JWKS fetches so tests can assert which path was taken.
type testIDP struct {
	key           *rsa.PrivateKey
	server        *httptest.Server
	discoveryHits atomic.Int32
	jwksHits      atomic.Int32
}

func newTestIDP(t *testing.T) *testIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pub := &key.PublicKey
	idp := &testIDP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		idp.discoveryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.server.URL,
			"jwks_uri":                              idp.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		idp.jwksHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	idp.server = httptest.NewUnstartedServer(mux)
	idp.server.Start()
	t.Cleanup(idp.server.Close)
	return idp
}

// issuer returns the IdP issuer URL (the httptest server URL).
func (idp *testIDP) issuer() string { return idp.server.URL }

// sign builds a signed JWT with the given claims. iss, aud, exp and iat are
// defaulted when absent.
func (idp *testIDP) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = idp.issuer()
	}
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = "mcp-server"
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = time.Now().Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(idp.key)
	require.NoError(t, err)
	return signed
}
