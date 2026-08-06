package s3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAllTools(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()

	tools, err := NewAllTools(ctx, configs)
	assert.NoError(t, err)
	assert.Len(t, tools, 5)

	names := make([]string, len(tools))
	for i, to := range tools {
		info, err := to.Info(ctx)
		assert.NoError(t, err)
		names[i] = info.Name
	}

	assert.Contains(t, names, "s3_list_buckets")
	assert.Contains(t, names, "s3_list_objects")
	assert.Contains(t, names, "s3_get_usage")
	assert.Contains(t, names, "s3_list_objects_with_size")
	assert.Contains(t, names, "s3_get_lifecycle")
}

func TestNewReadOnlyTools(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()

	tools, err := NewReadOnlyTools(ctx, configs)
	assert.NoError(t, err)
	assert.Len(t, tools, 5)
}

func TestWriteToolNames(t *testing.T) {
	names := WriteToolNames()
	assert.Nil(t, names)
}

func TestExtractWriteToolNames(t *testing.T) {
	ctx := context.Background()
	names, err := ExtractWriteToolNames(ctx, testConfigs())
	assert.NoError(t, err)
	assert.Nil(t, names)
}
