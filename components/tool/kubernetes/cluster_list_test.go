package kubernetes

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"
)

func TestClusterListTool(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"prod":    {Config: &rest.Config{}},
		"staging": {Config: &rest.Config{}},
	}

	tool, err := NewClusterListTool(ctx, configs)
	assert.NoError(t, err)

	// Verify Info returns the tool metadata.
	info, err := tool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "kubernetes_cluster_list", info.Name)

	// Empty string arguments — this was the bug: sonic.UnmarshalString("", ...) fails.
	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)
	assert.ElementsMatch(t, []string{"prod", "staging"}, outputs)

	// Empty JSON object arguments — also valid for a no-param tool.
	result, err = tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)
	assert.ElementsMatch(t, []string{"prod", "staging"}, outputs)
}
