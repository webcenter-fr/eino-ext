package github

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestInstanceListTool(t *testing.T) {
	ctx := context.Background()

	knownInstances := []string{"prod", "staging", "dev"}

	tool, err := newInstanceListTool(ctx, knownInstances)
	assert.NoError(t, err)

	// Verify Info returns the tool metadata.
	info, err := tool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "github_instance_list", info.Name)

	// Empty string arguments — this was the bug: sonic.UnmarshalString("", ...) fails.
	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 3)
	assert.ElementsMatch(t, []string{"prod", "staging", "dev"}, outputs)

	// Empty JSON object arguments — also valid for a no-param tool.
	result, err = tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 3)
	assert.ElementsMatch(t, []string{"prod", "staging", "dev"}, outputs)
}
