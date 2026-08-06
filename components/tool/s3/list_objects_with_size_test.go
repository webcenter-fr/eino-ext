package s3

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestListObjectsWithSizeTool(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newListObjectsWithSizeToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsWithSizeParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 6)

	assert.Equal(t, "backups/db-backup.tar.gz", entries[0].Key)

	assert.NotEmpty(t, entries[0].SizeHuman)
	assert.Greater(t, entries[0].Size, entries[len(entries)-1].Size)
}

func TestListObjectsWithSizeToolLimit(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newListObjectsWithSizeToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsWithSizeParams{Instance: "prod-logs", MaxKeys: 3}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestListObjectsWithSizeToolFilter(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newListObjectsWithSizeToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsWithSizeParams{Instance: "prod-logs", Filter: "logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestListObjectsWithSizeToolSortByName(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newListObjectsWithSizeToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsWithSizeParams{Instance: "prod-logs", SortBy: "alphanumeric"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)
	assert.Equal(t, "backups/db-backup.tar.gz", entries[0].Key)
}
