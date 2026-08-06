package s3

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestListObjectsTool(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		params        *ListObjectsParams
		expectedCount int
		checkFirst    func(t *testing.T, entries []objectEntry)
	}{
		{
			name: "list all objects default sort",
			params: &ListObjectsParams{
				Instance: "prod-logs",
			},
			expectedCount: 6,
			checkFirst: func(t *testing.T, entries []objectEntry) {
				assert.Equal(t, "backups/db-backup.tar.gz", entries[0].Key)
			},
		},
		{
			name: "sort by size descending",
			params: &ListObjectsParams{
				Instance: "prod-logs",
				SortBy:   "size",
			},
			expectedCount: 6,
			checkFirst: func(t *testing.T, entries []objectEntry) {
				assert.Equal(t, "backups/db-backup.tar.gz", entries[0].Key)
			},
		},
		{
			name: "sort by last_modified",
			params: &ListObjectsParams{
				Instance: "prod-logs",
				SortBy:   "last_modified",
			},
			expectedCount: 6,
			checkFirst: func(t *testing.T, entries []objectEntry) {
				assert.Equal(t, "logs/2024/access.log", entries[0].Key)
			},
		},
		{
			name: "max keys limit",
			params: &ListObjectsParams{
				Instance: "prod-logs",
				MaxKeys:  2,
			},
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs := testConfigs()
			mc := newMockListObjectsClient(makeTestObjects(), nil, false)
			tool, err := newListObjectsToolWithClients(configs, map[string]Client{"prod-logs": mc})
			assert.NoError(t, err)

			result, err := tool.InvokableRun(ctx, mustMarshal(t, tt.params))
			assert.NoError(t, err)

			var entries []objectEntry
			err = json.Unmarshal([]byte(result), &entries)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedCount, len(entries))

			if tt.checkFirst != nil {
				tt.checkFirst(t, entries)
			}
		})
	}
}

func TestListObjectsToolInvalidParams(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()

	tool, err := NewListObjectsTool(ctx, configs)
	assert.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance":"unknown"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestListObjectsToolWithFilter(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), nil, false)
	tool, err := newListObjectsToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsParams{
		Instance: "prod-logs",
		Filter:   "access",
	}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "logs/2024/access.log", entries[0].Key)
}

func TestListObjectsToolWithDirectories(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()
	mc := newMockListObjectsClient(makeTestObjects(), makeCommonPrefixes(), false)
	tool, err := newListObjectsToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &ListObjectsParams{
		Instance:  "prod-logs",
		Delimiter: "/",
	}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var entries []objectEntry
	err = json.Unmarshal([]byte(result), &entries)
	assert.NoError(t, err)

	assert.Greater(t, len(entries), 6)

	hasDirs := false
	for _, e := range entries {
		if e.IsDir {
			hasDirs = true
			break
		}
	}
	assert.True(t, hasDirs, "should have directory entries")
}

func TestSortObjectEntries(t *testing.T) {
	entries := []objectEntry{
		{Key: "c.txt", Size: 100},
		{Key: "a.txt", Size: 300},
		{Key: "b.txt", Size: 200},
	}

	sortObjectEntries(entries, SortAlphanumeric)
	assert.Equal(t, "a.txt", entries[0].Key)

	sortObjectEntries(entries, SortSize)
	assert.Equal(t, "a.txt", entries[0].Key)
	assert.Equal(t, "b.txt", entries[1].Key)
	assert.Equal(t, "c.txt", entries[2].Key)
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "512 B", humanSize(512))
	assert.Equal(t, "1.0 kB", humanSize(1024))
}
