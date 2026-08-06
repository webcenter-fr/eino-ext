package s3

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestBucketListTool(t *testing.T) {
	ctx := context.Background()

	configs := testConfigs()

	tool, err := NewBucketListTool(ctx, configs)
	assert.NoError(t, err)

	info, err := tool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "s3_list_buckets", info.Name)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []bucketListItem
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)

	var names []string
	for _, o := range outputs {
		names = append(names, o.Name)
	}
	assert.ElementsMatch(t, []string{"prod-logs", "staging-data"}, names)

	for _, o := range outputs {
		assert.NotEmpty(t, o.BucketName)
		assert.NotEmpty(t, o.Description)
	}
}

func TestBucketListToolEmptyConfigs(t *testing.T) {
	ctx := context.Background()

	tool, err := NewBucketListTool(ctx, Configs{})
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []bucketListItem
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Empty(t, outputs)
}

func TestBucketListToolSorted(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"zeta":  {BucketName: "b", AccessKey: "a", SecretKey: "s", Description: "d"},
		"alpha": {BucketName: "b", AccessKey: "a", SecretKey: "s", Description: "d"},
		"mid":   {BucketName: "b", AccessKey: "a", SecretKey: "s", Description: "d"},
	}

	tool, err := NewBucketListTool(ctx, configs)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	var outputs []bucketListItem
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)

	var names []string
	for _, o := range outputs {
		names = append(names, o.Name)
	}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, names)
}
