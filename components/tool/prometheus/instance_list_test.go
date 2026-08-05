package prometheus

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestInstanceListTool(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"prod":    {Address: "http://prod:9090"},
		"staging": {Address: "http://staging:9090"},
	}

	tool, err := NewInstanceListTool(ctx, configs)
	assert.NoError(t, err)

	info, err := tool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "prometheus_instance_list", info.Name)

	// Empty string arguments — must not fail (EmptyJSONUnmarshaler handles "").
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

func TestInstanceListToolEmptyConfigs(t *testing.T) {
	ctx := context.Background()

	tool, err := NewInstanceListTool(ctx, Configs{})
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Empty(t, outputs)
}

func TestInstanceListToolNilConfigs(t *testing.T) {
	ctx := context.Background()

	tool, err := NewInstanceListTool(ctx, nil)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Empty(t, outputs)
}

func TestInstanceListToolSorted(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"zeta":  {Address: "http://zeta:9090"},
		"alpha": {Address: "http://alpha:9090"},
		"mid":   {Address: "http://mid:9090"},
	}

	tool, err := NewInstanceListTool(ctx, configs)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, outputs)
}
