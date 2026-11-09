package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

const testReceiptSecret = "test-receipt-secret"

func approvalTestServer(t *testing.T, argsHash string) *httptest.Server {
	t.Helper()
	signer, err := mcpapproval.NewReceiptSigner(mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)})
	require.NoError(t, err)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/approvals/a1/decision":
			token, _, err := signer.Issue(mcpapproval.ReceiptClaims{
				ApprovalID: "a1", ServerName: "grafana", ToolName: "write_tool", Instance: "prod",
				RequesterSub: "alice", ApproverSub: "bob", ArgsHash: argsHash, Decision: "approve",
			})
			require.NoError(t, err)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "approved", "receipt": token})
		default:
			http.NotFound(w, r)
		}
	}))
}

func urlElicitationClient(t *testing.T, elicited *bool) *mcpsdk.ClientOptions {
	t.Helper()
	return &mcpsdk.ClientOptions{
		Capabilities: &mcpsdk.ClientCapabilities{
			Elicitation: &mcpsdk.ElicitationCapabilities{URL: &mcpsdk.URLElicitationCapabilities{}},
		},
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			if elicited != nil {
				*elicited = true
			}
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	}
}

func TestApprovalURLAuthorizerHappyPath(t *testing.T) {
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	srv := approvalTestServer(t, hash)
	defer srv.Close()

	elicited := false
	session := captureSession(t, urlElicitationClient(t, &elicited))
	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: session,
		identity: &Identity{User: "alice"}, instance: "prod", timeout: 2 * time.Second,
	}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "write_tool", args))
	assert.True(t, elicited)
	require.NotNil(t, a.LastReceipt())
	assert.Equal(t, "bob", a.LastReceipt().ApproverSub)
}

func TestApprovalURLAuthorizerFallbackWithoutURLMode(t *testing.T) {
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	srv := approvalTestServer(t, hash)
	defer srv.Close()

	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: session,
		identity: &Identity{User: "alice"}, instance: "prod", timeout: time.Second,
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", args)
	require.Error(t, err)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.Contains(t, err.Error(), "approvalId a1")
	assert.Contains(t, err.Error(), "https://approve.example/a1")
}

func TestApprovalURLAuthorizerFailsClosedOnOutage(t *testing.T) {
	elicited := false
	session := captureSession(t, urlElicitationClient(t, &elicited))
	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: "http://127.0.0.1:1", ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: session,
		identity: &Identity{User: "alice"}, instance: "prod", timeout: time.Second,
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", json.RawMessage(`{"cluster":"prod"}`))
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.False(t, elicited, "an outage must never downgrade to elicitation")
}

func TestApprovalURLAuthorizerApprovalIDPath(t *testing.T) {
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	srv := approvalTestServer(t, hash)
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", approvalID: "a1",
		instance: "prod", timeout: 2 * time.Second,
	}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "write_tool", args))
	require.NotNil(t, a.LastReceipt())
}

func TestApprovalURLAuthorizerRejectsHashMismatch(t *testing.T) {
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	srv := approvalTestServer(t, "different-hash")
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", approvalID: "a1",
		instance: "prod", timeout: 2 * time.Second,
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", args)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
}

func TestApprovalURLAuthorizerRequesterSubFromIdentitySub(t *testing.T) {
	// The requester.sub sent to the approval service must be the stable OIDC
	// subject (Identity.Sub), not the display name (Identity.User), so the
	// approval service's anti-phishing self-approve check compares like-for-like.
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)

	var got mcpapproval.RegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/approvals/a1/decision":
			_, _ = w.Write([]byte(`{"state":"rejected"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: captureSession(t, urlElicitationClient(t, nil)),
		identity: &Identity{User: "alice", Sub: "user-123"}, instance: "prod", timeout: time.Second,
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", args)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.Equal(t, "user-123", got.Requester.Sub, "requester.sub must be the OIDC sub, not the display name")
	assert.Equal(t, "alice", got.Requester.User)
	assert.Equal(t, hash, got.ArgsHash)
}

func TestApprovalURLAuthorizerRequesterSubFallsBackToUser(t *testing.T) {
	// Without a distinct subject (e.g. a local token identity), requester.sub
	// falls back to the user name.
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	var got mcpapproval.RegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/approvals/a1/decision":
			_, _ = w.Write([]byte(`{"state":"rejected"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: captureSession(t, urlElicitationClient(t, nil)),
		identity: &Identity{User: "alice"}, instance: "prod", timeout: time.Second,
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", args)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.Equal(t, "alice", got.Requester.Sub, "requester.sub must fall back to the user name")
}

func TestApprovalURLAuthorizerOnRequested(t *testing.T) {
	// Registering a new approval must invoke the onRequested callback so the
	// caller can emit a PhaseApprovalRequested audit event.
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	srv := approvalTestServer(t, hash)
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	var requestedID string
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: captureSession(t, urlElicitationClient(t, nil)),
		identity: &Identity{User: "alice"}, instance: "prod", timeout: 2 * time.Second,
		onRequested: func(id string) { requestedID = id },
	}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "write_tool", args))
	assert.Equal(t, "a1", requestedID)
}

func TestApprovalURLAuthorizerLargePayload(t *testing.T) {
	// The service path has no argument-size rejection limit: a payload well
	// above the elicitation display budget must still register and approve.
	args, err := json.Marshal(map[string]any{
		"cluster":   "prod",
		"confirmed": true,
		"dashboard": strings.Repeat("x", 2*ApprovalDisplayBudget),
	})
	require.NoError(t, err)
	require.Greater(t, len(args), ApprovalDisplayBudget)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	srv := approvalTestServer(t, hash)
	defer srv.Close()

	elicited := false
	session := captureSession(t, urlElicitationClient(t, &elicited))
	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: session,
		identity: &Identity{User: "alice"}, instance: "prod", timeout: 2 * time.Second,
	}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "write_tool", args))
	assert.True(t, elicited)
}

func TestApprovalURLAuthorizerPreviewFailureIsWarning(t *testing.T) {
	// A failing dry-run preview must be surfaced as a warning on the registered
	// approval, never treated as an approval. The human still decides, and a
	// rejection denies execution.
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	signer, err := mcpapproval.NewReceiptSigner(mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)})
	require.NoError(t, err)

	var got mcpapproval.RegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/approvals/a1/decision":
			_, _ = w.Write([]byte(`{"state":"rejected"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_ = signer

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		Receipt: mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)
	a := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: captureSession(t, urlElicitationClient(t, nil)),
		identity: &Identity{User: "alice"}, instance: "prod", timeout: time.Second,
		preview: func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New("preview boom")
		},
	}
	err = a.AuthorizeExecute(context.Background(), "write_tool", args)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.Equal(t, "preview boom", got.PreviewWarning)
	assert.Empty(t, got.Preview)
	assert.Equal(t, hash, got.ArgsHash)
}

func TestApprovalURLAuthorizerTimeoutThenApprovalIDRetry(t *testing.T) {
	// The MCP timeout may expire while the record is still pending; a later
	// re-call carrying approvalId must succeed once the human decides.
	args := json.RawMessage(`{"cluster":"prod","confirmed":true}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	signer, err := mcpapproval.NewReceiptSigner(mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)})
	require.NoError(t, err)

	var approved atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/approvals/a1/decision":
			if !approved.Load() {
				_, _ = w.Write([]byte(`{"state":"pending"}`))
				return
			}
			token, _, err := signer.Issue(mcpapproval.ReceiptClaims{
				ApprovalID: "a1", ServerName: "grafana", ToolName: "write_tool", Instance: "prod",
				RequesterSub: "alice", ApproverSub: "bob", ArgsHash: hash, Decision: "approve",
			})
			require.NoError(t, err)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "approved", "receipt": token})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := mcpapproval.NewClient(mcpapproval.Config{
		BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token",
		PollInterval: 10 * time.Millisecond,
		Receipt:      mcpapproval.ReceiptConfig{Secret: []byte(testReceiptSecret)},
	})
	require.NoError(t, err)

	first := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", session: captureSession(t, urlElicitationClient(t, nil)),
		identity: &Identity{User: "alice"}, instance: "prod", timeout: 50 * time.Millisecond,
	}
	err = first.AuthorizeExecute(context.Background(), "write_tool", args)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)

	approved.Store(true)
	retry := &ApprovalURLAuthorizer{
		client: client, serverName: "grafana", approvalID: "a1",
		instance: "prod", timeout: 2 * time.Second,
	}
	require.NoError(t, retry.AuthorizeExecute(context.Background(), "write_tool", args))
	require.NotNil(t, retry.LastReceipt())
}
