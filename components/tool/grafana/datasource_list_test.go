package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestNewDataSourceListToolRequiresConfigs(t *testing.T) {
	_, err := NewDataSourceListTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestDataSourceListToolInvoke(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"uid":"ds-prom","name":"Prometheus","type":"prometheus","url":"http://prom:9090","access":"proxy","isDefault":true,"version":3,"jsonData":{"timeInterval":"15s","httpHeaderValue":"secret-bearer"}},
			{"id":2,"uid":"ds-loki","name":"Loki","type":"loki","url":"http://loki:3100","access":"proxy","isDefault":false,"version":1,"jsonData":{"maxLines":1000}}
		]`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	tool, err := NewDataSourceListTool(ctx, Configs{"test": {URL: server.URL}})
	assert.NoError(t, err)

	tests := []struct {
		name    string
		params  string
		wantErr bool
		wantLen int
		check   func(t *testing.T, result string)
	}{
		{
			name:    "list all",
			params:  `{"instance":"test"}`,
			wantLen: 2,
			check: func(t *testing.T, result string) {
				var outputs []DataSourceListOutput
				assert.NoError(t, json.Unmarshal([]byte(result), &outputs))
				assert.Equal(t, "ds-prom", outputs[0].UID)
				assert.True(t, outputs[0].IsDefault)
				assert.NotContains(t, result, "secret-bearer")
				assert.Contains(t, result, `\u003credacted\u003e`)
			},
		},
		{
			name:    "filter",
			params:  `{"instance":"test","filter":"loki"}`,
			wantLen: 1,
			check: func(t *testing.T, result string) {
				assert.Contains(t, result, "Loki")
				assert.NotContains(t, result, "Prometheus")
			},
		},
		{
			name:    "unknown instance",
			params:  `{"instance":"invalid"}`,
			wantErr: true,
		},
		{
			name:    "missing instance",
			params:  `{}`,
			wantErr: true,
		},
		{
			name:    "invalid filter regex",
			params:  `{"instance":"test","filter":"(?=...)"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.InvokableRun(ctx, tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			if tt.wantLen > 0 {
				var outputs []DataSourceListOutput
				assert.NoError(t, json.Unmarshal([]byte(result), &outputs))
				assert.Len(t, outputs, tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestDataSourceListOutputExcludesSecrets(t *testing.T) {
	out := DataSourceListOutput{
		ID:       1,
		UID:      "u",
		Name:     "n",
		Type:     "prometheus",
		JSONData: map[string]any{"httpHeaderValue": "x", "timeField": "@timestamp"},
	}
	data, err := json.Marshal(out)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), `"password"`)
	assert.NotContains(t, string(data), `"secureJsonFields"`)
	assert.True(t, strings.Contains(string(data), `"timeField"`))
}
