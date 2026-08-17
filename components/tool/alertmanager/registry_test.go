package alertmanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testConfigs() Configs {
	return Configs{
		"t": {Address: "http://localhost:9093"},
	}
}

func TestNewAllTools(t *testing.T) {
	ctx := context.Background()

	tools, err := NewAllTools(ctx, testConfigs())
	assert.NoError(t, err)
	assert.Len(t, tools, 3)

	names := make([]string, len(tools))
	for i, to := range tools {
		info, err := to.Info(ctx)
		assert.NoError(t, err)
		names[i] = info.Name
	}

	assert.Contains(t, names, instanceListToolName)
	assert.Contains(t, names, alertToolName)
	assert.Contains(t, names, alertWriteToolName)
}

func TestNewReadOnlyTools(t *testing.T) {
	ctx := context.Background()

	tools, err := NewReadOnlyTools(ctx, testConfigs())
	assert.NoError(t, err)
	assert.Len(t, tools, 3)
}

func TestWriteToolNames(t *testing.T) {
	names := WriteToolNames()
	assert.NotNil(t, names)
	assert.Len(t, names, 0)
}

func TestExtractWriteToolNames(t *testing.T) {
	ctx := context.Background()
	names, err := ExtractWriteToolNames(ctx, testConfigs())
	assert.NoError(t, err)
	assert.NotNil(t, names)
	assert.Len(t, names, 0)
}
