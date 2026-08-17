package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
)

// mockTargetAPI embeds promapi.API so only Targets needs to be implemented.
// Calling any other method panics (nil pointer), which is fine for these tests.
type mockTargetAPI struct {
	v1.API
	targetsResult v1.TargetsResult
	targetsErr    error
}

func (m *mockTargetAPI) Targets(ctx context.Context) (v1.TargetsResult, error) {
	return m.targetsResult, m.targetsErr
}

func newTargetListToolWithMock(mock v1.API) *TargetListTool {
	return &TargetListTool{
		baseTool: &baseTool{
			clients:        map[string]v1.API{"prod": mock},
			knownInstances: []string{"prod"},
		},
	}
}

func TestTargetListTool(t *testing.T) {
	now := time.Now().UTC()
	upTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "node", "instance": "10.0.0.1:9100"},
		ScrapePool:         "node/10.0.0.1:9100",
		ScrapeURL:          "http://10.0.0.1:9100/metrics",
		Health:             v1.HealthGood,
		LastError:          "",
		LastScrape:         now,
		LastScrapeDuration: 0.0123,
	}
	downTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "kubelet", "instance": "10.0.0.2:10250"},
		ScrapePool:         "kubelet/10.0.0.2:10250",
		ScrapeURL:          "https://10.0.0.2:10250/metrics",
		Health:             v1.HealthBad,
		LastError:          "connection refused",
		LastScrape:         time.Time{},
		LastScrapeDuration: 0,
	}

	tests := []struct {
		name        string
		mock        *mockTargetAPI
		params      *TargetListParams
		wantCount   int
		wantErr     bool
		errContains string
	}{
		{
			name:      "happy path returns all active targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod"},
			wantCount: 2,
		},
		{
			name:      "health filter up returns only up targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Health: "up"},
			wantCount: 1,
		},
		{
			name:      "health filter down returns only down targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Health: "down"},
			wantCount: 1,
		},
		{
			name:      "scrapePool filter exact match",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", ScrapePool: "kubelet/10.0.0.2:10250"},
			wantCount: 1,
		},
		{
			name:      "scrapePool filter no match returns empty",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", ScrapePool: "nonexistent"},
			wantCount: 0,
		},
		{
			name:      "regex filter on scrapeUrl",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Filter: `10\.0\.0\.1`},
			wantCount: 1,
		},
		{
			name:      "empty results returns empty array",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: nil}},
			params:    &TargetListParams{Instance: "prod"},
			wantCount: 0,
		},
		{
			name:        "missing instance returns error",
			mock:        &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget}}},
			params:      &TargetListParams{Instance: "nonexistent"},
			wantErr:     true,
			errContains: "nonexistent",
		},
		{
			name:        "invalid health value fails validation",
			mock:        &mockTargetAPI{},
			params:      &TargetListParams{Instance: "prod", Health: "broken"},
			wantErr:     true,
			errContains: "invalid parameters",
		},
		{
			name:        "api error propagates",
			mock:        &mockTargetAPI{targetsErr: context.DeadlineExceeded},
			params:      &TargetListParams{Instance: "prod"},
			wantErr:     true,
			errContains: "failed to list targets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := newTargetListToolWithMock(tt.mock)
			result, err := tool.Invoke(context.Background(), tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			assert.NoError(t, err)

			var outputs []TargetListOutput
			err = json.Unmarshal([]byte(result), &outputs)
			assert.NoError(t, err)
			assert.Len(t, outputs, tt.wantCount)
		})
	}
}

func TestTargetListToolOutputFields(t *testing.T) {
	// Verify the formatted string fields render as expected.
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	tgt := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "node"},
		ScrapePool:         "node/x",
		ScrapeURL:          "http://x/metrics",
		Health:             v1.HealthGood,
		LastError:          "",
		LastScrape:         ts,
		LastScrapeDuration: 1.5,
	}
	tool := newTargetListToolWithMock(&mockTargetAPI{
		targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{tgt}},
	})
	result, err := tool.Invoke(context.Background(), &TargetListParams{Instance: "prod"})
	assert.NoError(t, err)

	var outputs []TargetListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 1)
	assert.Equal(t, "up", outputs[0].Health)
	assert.Equal(t, "node/x", outputs[0].ScrapePool)
	assert.Equal(t, "http://x/metrics", outputs[0].ScrapeUrl)
	assert.Equal(t, "2024-01-02T03:04:05Z", outputs[0].LastScrape)
	assert.Equal(t, "1.5s", outputs[0].LastScrapeDuration)
}

func TestTargetListToolConstructor(t *testing.T) {
	// Constructor with empty configs should succeed (no API calls at construction time).
	tool, err := NewTargetListTool(context.Background(), Configs{})
	assert.NoError(t, err)
	assert.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "prometheus_target_list", info.Name)
}

func TestTargetListToolNilConfigs(t *testing.T) {
	tool, err := NewTargetListTool(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, tool)
}

func TestTargetListToolRedactsScrapeURLCredentials(t *testing.T) {
	// Scrape URLs with embedded basic-auth credentials must have the userinfo
	// stripped before being exposed in tool output (CWE-200).
	credTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "node"},
		ScrapePool:         "node/10.0.0.1:9100",
		ScrapeURL:          "http://exporter:s3cr3t@10.0.0.1:9100/metrics",
		Health:             v1.HealthGood,
		LastScrape:         time.Time{},
		LastScrapeDuration: 0,
	}
	plainTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "kubelet"},
		ScrapePool:         "kubelet/10.0.0.2:10250",
		ScrapeURL:          "https://10.0.0.2:10250/metrics?token=keep",
		Health:             v1.HealthGood,
		LastScrape:         time.Time{},
		LastScrapeDuration: 0,
	}
	tool := newTargetListToolWithMock(&mockTargetAPI{
		targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{credTarget, plainTarget}},
	})
	result, err := tool.Invoke(context.Background(), &TargetListParams{Instance: "prod"})
	assert.NoError(t, err)

	var outputs []TargetListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)

	// Credentials in userinfo must be redacted.
	assert.Equal(t, "http://10.0.0.1:9100/metrics", outputs[0].ScrapeUrl)
	assert.NotContains(t, outputs[0].ScrapeUrl, "s3cr3t")
	assert.NotContains(t, outputs[0].ScrapeUrl, "exporter:")
	// Non-userinfo parts (host, path, query) are preserved.
	assert.Equal(t, "https://10.0.0.2:10250/metrics?token=keep", outputs[1].ScrapeUrl)
}

func TestRedactScrapeURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http://user:pass@host:9100/metrics", "http://host:9100/metrics"},
		{"http://user@host:9100/metrics", "http://host:9100/metrics"},
		{"http://host:9100/metrics", "http://host:9100/metrics"},
		{"https://t:a@host/metrics?x=1", "https://host/metrics?x=1"},
		{"not a url", "not a url"},
		{"", ""},
		{"10.0.0.1:9100", "10.0.0.1:9100"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, redactScrapeURL(tt.in), "input=%q", tt.in)
	}
}
