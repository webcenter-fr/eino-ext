package s3

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestGetUsageTool(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newGetUsageToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetUsageParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var out usageOutput
	err = json.Unmarshal([]byte(result), &out)
	assert.NoError(t, err)
	assert.Equal(t, "prod-logs", out.Bucket)
	assert.Equal(t, int64(6), out.TotalObjects)
	assert.Greater(t, out.TotalSizeBytes, int64(0))
	assert.NotEmpty(t, out.TotalSizeHuman)
}

func TestGetUsageToolEmpty(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockEmptyListClient()
	tool, err := newGetUsageToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetUsageParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var out usageOutput
	err = json.Unmarshal([]byte(result), &out)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), out.TotalObjects)
	assert.Equal(t, int64(0), out.TotalSizeBytes)
}

func TestGetUsageToolInvalidInstance(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()

	tool, err := NewGetUsageTool(ctx, configs)
	assert.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance":"unknown"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
