package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func TestInputSchemaJSON(t *testing.T) {
	info := &schema.ToolInfo{
		Name: "write_tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"cluster": {Type: schema.String, Desc: "target cluster", Required: true},
			"dryRun":  {Type: schema.Boolean, Desc: "preview only"},
		}),
	}
	m, err := inputSchemaJSON(info)
	require.NoError(t, err)
	schemaMap, ok := m.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schemaMap["type"])
	props, ok := schemaMap["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "cluster")
	assert.Contains(t, props, "dryRun")
	assert.Contains(t, schemaMap["required"], "cluster")

	// Tool without params (nil ParamsOneOf) → empty object schema.
	m, err = inputSchemaJSON(&schema.ToolInfo{Name: "no_params"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "object"}, m)
}

func TestAnnotationsFor(t *testing.T) {
	read := annotationsFor(false)
	assert.True(t, read.ReadOnlyHint)
	assert.Nil(t, read.DestructiveHint)
	assert.Nil(t, read.OpenWorldHint)

	write := annotationsFor(true)
	assert.False(t, write.ReadOnlyHint)
	require.NotNil(t, write.DestructiveHint)
	assert.True(t, *write.DestructiveHint)
	require.NotNil(t, write.OpenWorldHint)
	assert.True(t, *write.OpenWorldHint)
}

func TestExtractInstance(t *testing.T) {
	assert.Equal(t, "prod", extractInstance(json.RawMessage(`{"cluster":"prod"}`), "cluster"))
	assert.Equal(t, "prod", extractInstance(json.RawMessage(`{"instance":"prod"}`), "instance"))
	assert.Equal(t, "", extractInstance(json.RawMessage(`{"other":"prod"}`), "cluster"))
	assert.Equal(t, "", extractInstance(json.RawMessage(`{"cluster":42}`), "cluster"))
	assert.Equal(t, "", extractInstance(json.RawMessage(`not json`), "cluster"))
	assert.Equal(t, "", extractInstance(json.RawMessage(`[1,2]`), "cluster"))
}

func TestHandlerReadTool(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "read_tool", run: func(context.Context, string) (string, error) {
		return "read result", nil
	}}
	s := newTestServer(t, &Config{
		Tools:     []tool.InvokableTool{ft},
		AuditSink: sink,
	})
	cs := connectInMemory(t, s, nil, nil)

	res := callTool(t, cs, "read_tool", map[string]any{"cluster": "prod"})
	assert.False(t, res.IsError)
	assert.Equal(t, "read result", resultText(t, res))
	assert.Equal(t, int32(1), ft.calls.Load())

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseRead, event.Phase)
	assert.Equal(t, "read_tool", event.ToolName)
	assert.Equal(t, "read result", event.Result)
	assert.True(t, event.PolicyPass)
	assert.Equal(t, "anonymous", event.Metadata["user"])
}

func TestHandlerWriteDryRun(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "write_tool", run: func(context.Context, string) (string, error) {
		return "preview", nil
	}}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  sink,
	})
	cs := connectInMemory(t, s, nil, nil)

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "dryRun": true})
	assert.False(t, res.IsError)
	assert.Equal(t, "preview"+safety.DryRunGuidance, resultText(t, res))
	assert.Equal(t, int32(1), ft.calls.Load())
	assert.False(t, ft.authorizedFor.Load(), "dry-run must not grant execution")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseDryRun, event.Phase)
}

func TestHandlerWriteDryRunPrecedence(t *testing.T) {
	// dryRun=true + confirmed=true → dry-run wins (gate rule 2 before rule 3);
	// no elicitation is requested (the client has no elicitation support, so
	// an approval request would fail the call).
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  newTestSink(t),
	})
	cs := connectInMemory(t, s, nil, &mcpsdk.ClientOptions{})

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "dryRun": true, "confirmed": true})
	assert.False(t, res.IsError)
	assert.Contains(t, resultText(t, res), safety.DryRunGuidance)
	assert.False(t, ft.authorizedFor.Load())
}

func TestHandlerWriteConfirmedAccepted(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "write_tool", run: func(context.Context, string) (string, error) {
		return "executed", nil
	}}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  sink,
	})
	cs := connectInMemory(t, s, nil, elicitationClient("accept"))

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "confirmed": true})
	assert.False(t, res.IsError)
	assert.Equal(t, "executed", resultText(t, res))
	assert.Equal(t, int32(1), ft.calls.Load())
	assert.True(t, ft.authorizedFor.Load(), "the ctx grant must be visible to the tool")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseExecute, event.Phase)
}

func TestHandlerWriteConfirmedDeclined(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  sink,
	})
	cs := connectInMemory(t, s, nil, elicitationClient("decline"))

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "confirmed": true})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), ErrApprovalDenied.Error())
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseRejected, event.Phase)
}

func TestHandlerWriteConfirmedNoElicitation(t *testing.T) {
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  newTestSink(t),
	})
	// Client without elicitation support: write tools are dry-run only.
	cs := connectInMemory(t, s, nil, &mcpsdk.ClientOptions{})

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "confirmed": true})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), safety.ErrExecutionNotAuthorized.Error())
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")
}

func TestHandlerWriteNoGateParams(t *testing.T) {
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:      []tool.InvokableTool{ft},
		WriteTools: []string{"write_tool"},
		AuditSink:  newTestSink(t),
	})
	cs := connectInMemory(t, s, nil, nil)

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), safety.ErrGateRequired.Error())
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")
}

func TestHandlerCustomExecutionAuthorizer(t *testing.T) {
	// A host-provided ExecutionAuthorizer decides instead of elicitation: the
	// client has no elicitation support, yet the write executes.
	ft := &fakeTool{name: "write_tool"}
	s := newTestServer(t, &Config{
		Tools:               []tool.InvokableTool{ft},
		WriteTools:          []string{"write_tool"},
		ExecutionAuthorizer: stubAuthorizer{},
		AuditSink:           newTestSink(t),
	})
	cs := connectInMemory(t, s, nil, &mcpsdk.ClientOptions{})

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "confirmed": true})
	assert.False(t, res.IsError)
	assert.Equal(t, int32(1), ft.calls.Load())
	assert.True(t, ft.authorizedFor.Load())
}

func TestHandlerRBACDenyInstance(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "write_tool"}
	id := &Identity{User: "alice"}
	s := newTestServer(t, &Config{
		Tools:         []tool.InvokableTool{ft},
		WriteTools:    []string{"write_tool"},
		LocalIdentity: id,
		Authorization: &AuthzConfig{Rules: []AccessRule{
			{Users: []string{"alice"}, Instances: []string{"prod"}},
		}},
		AuditSink: sink,
	})
	cs := connectInMemory(t, s, id, nil)

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "dev", "dryRun": true})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), ErrAccessDenied.Error())
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseRejected, event.Phase)
	assert.Equal(t, "alice", event.Metadata["user"])
}

func TestHandlerRBACDenyWriteOpForReadOnlyIdentity(t *testing.T) {
	// The per-identity server is the first layer (a read-only identity does not
	// even see write tools); the handler re-checks authorization on every call
	// (defense in depth). Here the server was built for a read-write identity
	// while the request identity is read-only: the handler must deny.
	ft := &fakeTool{name: "write_tool"}
	readOnly := &Identity{User: "reader"}
	s := newTestServer(t, &Config{
		Tools:         []tool.InvokableTool{ft},
		WriteTools:    []string{"write_tool"},
		LocalIdentity: readOnly,
		Authorization: &AuthzConfig{Rules: []AccessRule{
			{Users: []string{"reader"}, Operations: []string{"read"}},
			{Users: []string{"writer"}},
		}},
		AuditSink: newTestSink(t),
	})
	cs := connectInMemory(t, s, &Identity{User: "writer"}, nil)

	res := callTool(t, cs, "write_tool", map[string]any{"cluster": "prod", "dryRun": true})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), ErrAccessDenied.Error())
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")
}

func TestHandlerPolicyDeny(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "read_tool"}
	s := newTestServer(t, &Config{
		Tools:     []tool.InvokableTool{ft},
		Policy:    stubPolicy{err: errors.New("policy says no")},
		AuditSink: sink,
	})
	cs := connectInMemory(t, s, nil, nil)

	res := callTool(t, cs, "read_tool", map[string]any{"cluster": "prod"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "policy says no")
	assert.Equal(t, int32(0), ft.calls.Load(), "the tool must not run")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseRejected, event.Phase)
	assert.False(t, event.PolicyPass)
}

func TestHandlerToolError(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "read_tool", run: func(context.Context, string) (string, error) {
		return "", errors.New("boom")
	}}
	s := newTestServer(t, &Config{
		Tools:     []tool.InvokableTool{ft},
		AuditSink: sink,
	})
	cs := connectInMemory(t, s, nil, nil)

	res := callTool(t, cs, "read_tool", map[string]any{"cluster": "prod"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "boom")

	event := nextAuditEvent(t, sink)
	assert.Equal(t, safety.PhaseRead, event.Phase)
	assert.Equal(t, "boom", event.Error)
}

func TestHandlerAuditIdentityMetadata(t *testing.T) {
	sink := newTestSink(t)
	ft := &fakeTool{name: "read_tool"}
	id := &Identity{User: "alice", Groups: []string{"dev"}}
	s := newTestServer(t, &Config{
		Tools:         []tool.InvokableTool{ft},
		LocalIdentity: id,
		AuditSink:     sink,
	})
	cs := connectInMemory(t, s, id, nil)

	res := callTool(t, cs, "read_tool", map[string]any{"cluster": "prod"})
	assert.False(t, res.IsError)

	event := nextAuditEvent(t, sink)
	assert.Equal(t, "alice", event.Metadata["user"])
	assert.Equal(t, "dev", event.Metadata["groups"])
}
