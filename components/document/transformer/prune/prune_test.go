package prune

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
)

func TestNewPruner(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{name: "nil config", config: nil, wantErr: false},
		{name: "default min length", config: &Config{MinContentLength: 1}, wantErr: false},
		{name: "custom min length", config: &Config{MinContentLength: 10}, wantErr: false},
		{name: "zero min length gets default", config: &Config{MinContentLength: 0}, wantErr: false},
		{name: "negative min length gets default", config: &Config{MinContentLength: -5}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPruner(context.Background(), tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, p)
		})
	}
}

func TestTransform(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		input          []*schema.Document
		wantLen        int
		wantContentIDs []string
		extraCheck      func(t *testing.T, result []*schema.Document)
	}{
		{
			name:           "empty input",
			config:         nil,
			input:          nil,
			wantLen:        0,
			wantContentIDs: nil,
		},
		{
			name:           "nil slice",
			config:         nil,
			input:          []*schema.Document{},
			wantLen:        0,
			wantContentIDs: nil,
		},
		{
			name:   "single doc kept",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "hello world", MetaData: map[string]any{"key": "val"}},
			},
			wantLen:        1,
			wantContentIDs: []string{"1"},
		},
		{
			name:   "single doc pruned - empty content",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: ""},
			},
			wantLen:        0,
			wantContentIDs: nil,
		},
		{
			name:   "single doc pruned - whitespace only",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "   \t\n  "},
			},
			wantLen:        0,
			wantContentIDs: nil,
		},
		{
			name:   "mixed kept and pruned",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "valid content"},
				{ID: "2", Content: ""},
				{ID: "3", Content: "also valid"},
				{ID: "4", Content: "   "},
			},
			wantLen:        2,
			wantContentIDs: []string{"1", "3"},
		},
		{
			name:   "UTF-8 content",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "", MetaData: map[string]any{"lang": "cn"}},
				{ID: "2", Content: "世界", MetaData: map[string]any{"lang": "cn"}},
				{ID: "3", Content: "привет", MetaData: map[string]any{"lang": "ru"}},
			},
			wantLen:        2,
			wantContentIDs: []string{"2", "3"},
		},
		{
			name:   "below threshold pruned",
			config: &Config{MinContentLength: 5},
			input: []*schema.Document{
				{ID: "1", Content: "hi"},
				{ID: "2", Content: "hello world"},
			},
			wantLen:        1,
			wantContentIDs: []string{"2"},
		},
		{
			name:   "at threshold kept",
			config: &Config{MinContentLength: 3},
			input: []*schema.Document{
				{ID: "1", Content: "abc"},
				{ID: "2", Content: "ab"},
			},
			wantLen:        1,
			wantContentIDs: []string{"1"},
		},
		{
			name:   "metadata preserved",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "hello", MetaData: map[string]any{"key": "val", "nested": map[string]any{"inner": 42}}},
			},
			wantLen:        1,
			wantContentIDs: []string{"1"},
			extraCheck: func(t *testing.T, result []*schema.Document) {
				assert.Equal(t, "val", result[0].MetaData["key"])
			},
		},
		{
			name:   "nil doc in slice skipped",
			config: nil,
			input: []*schema.Document{
				{ID: "1", Content: "hello"},
				nil,
				{ID: "2", Content: "world"},
			},
			wantLen:        2,
			wantContentIDs: []string{"1", "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPruner(context.Background(), tt.config)
			assert.NoError(t, err)

			result, err := p.Transform(context.Background(), tt.input)
			assert.NoError(t, err)
			assert.Len(t, result, tt.wantLen)

			if tt.wantContentIDs == nil {
				return
			}

			gotIDs := make([]string, len(result))
			for i, doc := range result {
				gotIDs[i] = doc.ID
			}
			assert.Equal(t, tt.wantContentIDs, gotIDs)

			if tt.name == "metadata preserved" {
				assert.Equal(t, "val", result[0].MetaData["key"])
			}
		})
	}
}
