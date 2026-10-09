package kubernetes_test

import (
	"context"
	"slices"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/webcenter-fr/eino-ext/components/mcp/kubernetes"
	k8stool "github.com/webcenter-fr/eino-ext/components/tool/kubernetes"
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

func dummyConfigs() k8stool.Configs {
	// Construction is lazy: no live cluster is needed at build time.
	return k8stool.Configs{"prod": {Config: &rest.Config{Host: "https://127.0.0.1:6443"}}}
}

func TestNewServer(t *testing.T) {
	s, err := kubernetes.NewServer(context.Background(), dummyConfigs(), runtime.NewScheme(), nil)
	require.NoError(t, err)
	require.NotNil(t, s)

	tools := listTools(t, connectInMemory(t, s, nil))

	// All read + write tools are exposed.
	for _, name := range []string{
		"kubernetes_cluster_list",
		"kubernetes_list",
		"kubernetes_describe",
		"kubernetes_pod_logs",
		"kubernetes_pod_exec",
		"kubernetes_resource_create",
		"kubernetes_resource_patch",
		"kubernetes_resource_delete",
		"kubernetes_resource_apply",
	} {
		assert.Contains(t, tools, name)
	}

	// Annotations: write tools are destructive, read tools are read-only.
	writeTools := k8stool.WriteToolNames()
	for name, tl := range tools {
		require.NotNil(t, tl.Annotations, name)
		if slices.Contains(writeTools, name) {
			assert.False(t, tl.Annotations.ReadOnlyHint, name)
			require.NotNil(t, tl.Annotations.DestructiveHint, name)
			assert.True(t, *tl.Annotations.DestructiveHint, name)
		} else {
			assert.True(t, tl.Annotations.ReadOnlyHint, name)
		}
	}
}

func TestInstanceParamRBAC(t *testing.T) {
	// The instance argument is "cluster": a rule allowing only cluster "prod"
	// denies a call targeting "dev".
	id := &mcpserver.Identity{User: "alice"}
	s, err := kubernetes.NewServer(context.Background(), dummyConfigs(), runtime.NewScheme(), &mcpserver.Config{
		LocalIdentity: id,
		Authorization: &mcpserver.AuthzConfig{Rules: []mcpserver.AccessRule{
			{Users: []string{"alice"}, Instances: []string{"prod"}},
		}},
	})
	require.NoError(t, err)

	cs := connectInMemory(t, s, id)

	// cluster=dev is not allowed → denied before the tool runs.
	res := callTool(t, cs, "kubernetes_resource_delete", map[string]any{"cluster": "dev", "dryRun": true})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "access denied")

	// cluster=prod is allowed → RBAC passes and the safety gate engages
	// (ErrGateRequired) before any cluster contact.
	res = callTool(t, cs, "kubernetes_resource_delete", map[string]any{"cluster": "prod"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "SAFETY GATE")
	assert.NotContains(t, resultText(t, res), "access denied")
}
