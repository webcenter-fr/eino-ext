package prometheus_test

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/components/mcp/prometheus"
	promtool "github.com/webcenter-fr/eino-ext/components/tool/prometheus"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// connectInMemory connects the per-identity MCP server to a client over
// in-memory transports and returns the client session.
func connectInMemory(t *testing.T, s *mcpserver.Server, id *mcpserver.Identity) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv, err := s.ServerFor(ctx, id)
	require.NoError(t, err)
	ct, st := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// listTools returns the listed tools keyed by name.
func listTools(t *testing.T, cs *mcpsdk.ClientSession) map[string]*mcpsdk.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	tools := make(map[string]*mcpsdk.Tool, len(res.Tools))
	for _, tl := range res.Tools {
		tools[tl.Name] = tl
	}
	return tools
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

func dummyConfigs() promtool.Configs {
	// Construction is lazy: no live Prometheus server is needed at build time.
	return promtool.Configs{"prod": {Address: "http://127.0.0.1:9090"}}
}

func TestNewServer(t *testing.T) {
	s, err := prometheus.NewServer(context.Background(), dummyConfigs(), nil)
	require.NoError(t, err)
	require.NotNil(t, s)

	tools := listTools(t, connectInMemory(t, s, nil))

	// All tools are exposed; Prometheus has no write tools, so every tool is
	// annotated read-only.
	for _, name := range []string{
		"prometheus_instance_list",
		"prometheus_metric",
		"prometheus_target_list",
	} {
		assert.Contains(t, tools, name)
	}
	assert.Empty(t, promtool.WriteToolNames())
	for name, tl := range tools {
		require.NotNil(t, tl.Annotations, name)
		assert.True(t, tl.Annotations.ReadOnlyHint, name)
	}
}

func TestInstanceParamRBAC(t *testing.T) {
	// The instance argument is "instance": a rule allowing only instance "prod"
	// denies a call targeting "dev".
	id := &mcpserver.Identity{User: "alice"}
	s, err := prometheus.NewServer(context.Background(), dummyConfigs(), &mcpserver.Config{
		LocalIdentity: id,
		Authorization: &mcpserver.AuthzConfig{Rules: []mcpserver.AccessRule{
			{Users: []string{"alice"}, Instances: []string{"prod"}},
		}},
	})
	require.NoError(t, err)

	cs := connectInMemory(t, s, id)

	// instance=dev is not allowed → denied before the tool runs.
	res := callTool(t, cs, "prometheus_metric", map[string]any{"instance": "dev", "query": "up"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "access denied")

	// instance=prod is allowed → RBAC passes and the tool runs (it fails to
	// reach the dummy server, which proves the call was not denied by RBAC).
	res = callTool(t, cs, "prometheus_metric", map[string]any{"instance": "prod", "query": "up"})
	assert.True(t, res.IsError)
	assert.NotContains(t, resultText(t, res), "access denied")
}
