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
	// Arguments larger than maxApprovalArgsLen are rejected WITHOUT eliciting:
	// the human could only review a truncated prefix while the full payload
	// would execute — the approval must cover exactly what will run.
	elicited := false
	session := captureSession(t, &mcpsdk.ClientOptions{
		ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			elicited = true
			return &mcpsdk.ElicitResult{Action: "accept"}, nil
		},
	})
	a := &ElicitationAuthorizer{session: session, timeout: time.Minute}
	oversized := json.RawMessage(`{"data":"` + strings.Repeat("x", maxApprovalArgsLen) + `"}`)
	err := a.AuthorizeExecute(context.Background(), "write_tool", oversized)
	assert.ErrorIs(t, err, safety.ErrExecutionNotAuthorized)
	assert.Contains(t, err.Error(), "exceeding the maximum approvable size")
	assert.False(t, elicited, "no elicitation request must be sent for oversized arguments")

	// Arguments at exactly the limit are still approvable.
	exact := json.RawMessage(`{"data":"` + strings.Repeat("x", maxApprovalArgsLen-len(`{"data":""}`)) + `"}`)
	err = a.AuthorizeExecute(context.Background(), "write_tool", exact)
	assert.NoError(t, err)
	assert.True(t, elicited)
}

func TestBuildApprovalMessage(t *testing.T) {
	msg := buildApprovalMessage(&Identity{User: "alice"}, "kubernetes_resource_delete", "prod",
		json.RawMessage(`{"cluster":"prod"}`))
	assert.Contains(t, msg, "kubernetes_resource_delete")
	assert.Contains(t, msg, "alice")
	assert.Contains(t, msg, "prod")
	assert.Contains(t, msg, `{"cluster":"prod"}`)

	// Nil identity → anonymous; empty instance → no Target line.
	msg = buildApprovalMessage(nil, "write_tool", "", json.RawMessage(`{}`))
	assert.Contains(t, msg, "anonymous")
	assert.NotContains(t, msg, "Target:")

	// Arguments longer than maxApprovalArgsLen are truncated.
	longArgs := json.RawMessage(`{"data":"` + strings.Repeat("x", 2*maxApprovalArgsLen) + `"}`)
	msg = buildApprovalMessage(&Identity{User: "alice"}, "write_tool", "prod", longArgs)
	assert.Contains(t, msg, "truncated")
	assert.Less(t, len(msg), maxApprovalArgsLen+200)
}
