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

func TestNewDataSourceToolRequiresConfigs(t *testing.T) {
	_, err := NewDataSourceTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestDataSourceToolInvoke(t *testing.T) {
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
	mux.HandleFunc("/api/datasources/uid/sec-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":1,"uid":"sec-1","orgId":1,"name":"Secure DS","type":"prometheus",
			"access":"proxy","url":"http://prom:9090","isDefault":true,
			"jsonData":{"timeInterval":"15s","httpHeaderValue":"secret-bearer"},
			"secureJsonFields":{"httpHeaderValue":true},"readOnly":false,"version":3,
			"password":"should-not-leak","basicAuthPassword":""
		}`))
	})
	mux.HandleFunc("/api/datasources/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"data source not found"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	tool, err := NewDataSourceTool(ctx, Configs{"test": {URL: server.URL}})
	assert.NoError(t, err)

	tests := []struct {
		name    string
		params  string
		wantErr bool
		check   func(t *testing.T, result string)
	}{
		{
			name:   "list all",
			params: `{"instance":"test"}`,
			check: func(t *testing.T, result string) {
				var outputs []DataSourceListOutput
				assert.NoError(t, json.Unmarshal([]byte(result), &outputs))
				assert.Len(t, outputs, 2)
				assert.Equal(t, "ds-prom", outputs[0].UID)
				assert.True(t, outputs[0].IsDefault)
				assert.NotContains(t, result, "secret-bearer")
				assert.Contains(t, result, `\u003credacted\u003e`)
			},
		},
		{
			name:   "list with filter",
			params: `{"instance":"test","filter":"loki"}`,
			check: func(t *testing.T, result string) {
				var outputs []DataSourceListOutput
				assert.NoError(t, json.Unmarshal([]byte(result), &outputs))
				assert.Len(t, outputs, 1)
				assert.Equal(t, "Loki", outputs[0].Name)
			},
		},
		{
			name:    "list unknown instance",
			params:  `{"instance":"invalid"}`,
			wantErr: true,
		},
		{
			name:    "list missing instance",
			params:  `{}`,
			wantErr: true,
		},
		{
			name:    "list invalid filter regex",
			params:  `{"instance":"test","filter":"(?=...)"}`,
			wantErr: true,
		},
		{
			name:   "describe existing",
			params: `{"instance":"test","uid":"sec-1"}`,
			check: func(t *testing.T, result string) {
				assert.Contains(t, result, `"sec-1"`)
				assert.Contains(t, result, `"prometheus"`)
				assert.Contains(t, result, `"timeInterval"`)
				assert.Contains(t, result, `\u003credacted\u003e`)
				assert.NotContains(t, result, "should-not-leak")
				assert.NotContains(t, result, "secret-bearer")
				assert.NotContains(t, result, `"secureJsonFields"`)
				assert.NotContains(t, result, `"password"`)

				var out DataSourceDescribeOutput
				assert.NoError(t, json.Unmarshal([]byte(result), &out))
				assert.Equal(t, "sec-1", out.UID)
			},
		},
		{
			name:    "describe nonexistent uid",
			params:  `{"instance":"test","uid":"nonexistent"}`,
			wantErr: true,
		},
		{
			name:    "describe unknown instance",
			params:  `{"instance":"invalid","uid":"sec-1"}`,
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
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestDataSourceDescribeNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"data source not found"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	tool, err := NewDataSourceTool(ctx, Configs{"test": {URL: server.URL}})
	assert.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance":"test","uid":"nonexistent"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
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

func TestDataSourceToolConstructor(t *testing.T) {
	tool, err := NewDataSourceTool(context.Background(), Configs{"t": {URL: "http://localhost"}})
	assert.NoError(t, err)
	assert.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "grafana_datasource", info.Name)
}
