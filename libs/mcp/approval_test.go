package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// captureSession connects a raw MCP server (with a single "capture" tool that
// records the server session) to a client with the given options, calls the
// tool, and returns the captured *mcpsdk.ServerSession. The session stays
// alive until the test ends.
func captureSession(t *testing.T, clientOpts *mcpsdk.ClientOptions) *mcpsdk.ServerSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "capture-server", Version: "0.0.0"}, nil)
	var captured *mcpsdk.ServerSession
	srv.AddTool(&mcpsdk.Tool{
		Name:        "capture",
		Description: "captures the server session",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		captured = req.Session
		return &mcpsdk.CallToolResult{}, nil
	})
	ct, st := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcpsdk.NewClient(testClientImpl, clientOpts)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	_, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capture", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.NotNil(t, captured)
	return captured
}

func TestElicitationAuthorizerAccept(t *testing.T) {
	var gotMessage string
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			gotMessage = req.Params.Message
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	a := &ElicitationAuthorizer{
		session:  session,
		identity: &Identity{User: "alice"},
		instance: "prod",
		timeout:  time.Minute,
	}
	err := a.AuthorizeExecute(context.Background(), "kubernetes_resource_delete",
		json.RawMessage(`{"cluster":"prod","confirmed":true}`))
	assert.NoError(t, err)

	// The human sees the tool name, the user, the target instance and the
	// ACTUAL arguments of the execution call.
	assert.Contains(t, gotMessage, "kubernetes_resource_delete")
	assert.Contains(t, gotMessage, "alice")
	assert.Contains(t, gotMessage, "prod")
	assert.Contains(t, gotMessage, `"confirmed":true`)
}

func TestElicitationAuthorizerDeclineCancel(t *testing.T) {
	for _, action := range []string{"decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			session := captureSession(t, elicitationClient(action))
			a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
			err := a.AuthorizeExecute(context.Background(), "write_tool", json.RawMessage(`{}`))
			assert.ErrorIs(t, err, ErrApprovalDenied)
		})
	}
}

func TestElicitationAuthorizerNoElicitationCapability(t *testing.T) {
	// A client without an ElicitationHandler does not advertise the elicitation
	// capability: the authorizer fails closed before even sending a request.
	session := captureSession(t, &mcpsdk.ClientOptions{})
	a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
	err := a.AuthorizeExecute(context.Background(), "write_tool", json.RawMessage(`{}`))
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
}

func TestElicitationAuthorizerNilSession(t *testing.T) {
	a := &ElicitationAuthorizer{}
	err := a.AuthorizeExecute(context.Background(), "write_tool", json.RawMessage(`{}`))
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
}

func TestElicitationAuthorizerTimeout(t *testing.T) {
	// The client's elicitation handler blocks until the context is cancelled;
	// the authorizer's timeout rejects the write (fail closed).
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(ctx context.Context, _ *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	a := &ElicitationAuthorizer{session: session, timeout: 50 * time.Millisecond}
	err := a.AuthorizeExecute(context.Background(), "write_tool", json.RawMessage(`{}`))
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
}

func TestElicitationAuthorizerOversizedArgs(t *testing.T) {
	// Arguments larger than the display budget are still approvable: the
	// elicitation IS sent, and the prompt discloses the withheld bytes and the
	// full argsHash instead of silently truncating or denying.
	var gotMessage string
	elicited := false
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			elicited = true
			gotMessage = req.Params.Message
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
	oversized := json.RawMessage(`{"data":"` + strings.Repeat("x", ApprovalDisplayBudget) + `"}`)
	err := a.AuthorizeExecute(context.Background(), "write_tool", oversized)
	assert.NoError(t, err)
	assert.True(t, elicited, "the elicitation must be sent for oversized arguments")
	assert.Contains(t, gotMessage, "showing 4000 of")
	assert.Contains(t, gotMessage, "remainder NOT displayed")
	assert.Contains(t, gotMessage, "SHA-256 of full arguments:")
	hash, hashErr := mcpapproval.ArgsHash(oversized)
	require.NoError(t, hashErr)
	assert.Contains(t, gotMessage, hash)
}

func TestBuildApprovalMessage(t *testing.T) {
	args := json.RawMessage(`{"cluster":"prod"}`)
	msg := buildApprovalMessage(&Identity{User: "alice"}, "kubernetes_resource_delete", "prod", args)
	assert.Contains(t, msg, "kubernetes_resource_delete")
	assert.Contains(t, msg, "alice")
	assert.Contains(t, msg, "prod")
	assert.Contains(t, msg, `{"cluster":"prod"}`)
	hash, err := mcpapproval.ArgsHash(args)
	require.NoError(t, err)
	assert.Contains(t, msg, "SHA-256 of full arguments: "+hash)

	// Nil identity → anonymous; empty instance → no Target line.
	msg = buildApprovalMessage(nil, "write_tool", "", json.RawMessage(`{}`))
	assert.Contains(t, msg, "anonymous")
	assert.NotContains(t, msg, "Target:")

	// Arguments longer than the display budget are truncated with disclosure.
	longArgs := json.RawMessage(`{"data":"` + strings.Repeat("x", 2*ApprovalDisplayBudget) + `"}`)
	msg = buildApprovalMessage(&Identity{User: "alice"}, "write_tool", "prod", longArgs)
	assert.Contains(t, msg, "showing 4000 of")
	assert.Contains(t, msg, "remainder NOT displayed")
	longHash, err := mcpapproval.ArgsHash(longArgs)
	require.NoError(t, err)
	assert.Contains(t, msg, longHash)
}

func TestApprovalRegressionGrafanaDashboard(t *testing.T) {
	// A realistic 6-panel dashboard serializes to ~6 KB, well above the old
	// 4000-byte rejection threshold. It must reach approval.
	panels := make([]map[string]any, 0, 6)
	for i := 0; i < 6; i++ {
		panels = append(panels, map[string]any{
			"id": i + 1, "title": strings.Repeat("panel-", 100),
			"type": "timeseries", "gridPos": map[string]any{"h": 8, "w": 12, "x": 0, "y": i * 8},
			"targets": []map[string]any{{"expr": strings.Repeat("rate(http_requests_total[5m])", 50)}},
		})
	}
	dashboard, err := json.Marshal(map[string]any{"title": "prod overview", "panels": panels})
	require.NoError(t, err)
	args, err := json.Marshal(map[string]any{
		"instance": "prod", "operation": "create", "dashboard": string(dashboard), "confirmed": true,
	})
	require.NoError(t, err)
	require.Greater(t, len(args), ApprovalDisplayBudget)

	var gotMessage string
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			gotMessage = req.Params.Message
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "grafana_dashboard_write", args))
	assert.Contains(t, gotMessage, "showing 4000 of")
	assert.Contains(t, gotMessage, "remainder NOT displayed")
}

func TestApprovalRegressionDeploymentLastApplied(t *testing.T) {
	// A Deployment carrying kubectl.kubernetes.io/last-applied-configuration
	// roughly doubles the args and used to be denied. It must reach approval.
	lastApplied := strings.Repeat(`{"apiVersion":"apps/v1","kind":"Deployment"},`, 100)
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{
			"name": "demo", "namespace": "default",
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": lastApplied,
			},
		},
		"spec": map[string]any{"replicas": 2},
	})
	require.NoError(t, err)
	args, err := json.Marshal(map[string]any{
		"cluster": "prod", "manifest": string(manifest), "confirmed": true,
	})
	require.NoError(t, err)
	require.Greater(t, len(args), ApprovalDisplayBudget)

	var gotMessage string
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			gotMessage = req.Params.Message
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
	require.NoError(t, a.AuthorizeExecute(context.Background(), "kubernetes_resource_apply", args))
	assert.Contains(t, gotMessage, "showing 4000 of")
	assert.Contains(t, gotMessage, "remainder NOT displayed")
}
