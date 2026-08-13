package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestNewDataSourceDescribeToolRequiresConfigs(t *testing.T) {
	_, err := NewDataSourceDescribeTool(context.Background(), Configs{})
	assert.Error(t, err)
}

func TestDataSourceDescribeToolInvoke(t *testing.T) {
	mux := http.NewServeMux()
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
	tool, err := NewDataSourceDescribeTool(ctx, Configs{"test": {URL: server.URL}})
	assert.NoError(t, err)

	tests := []struct {
		name    string
		params  string
		wantErr bool
		check   func(t *testing.T, result string)
	}{
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
			name:    "nonexistent uid",
			params:  `{"instance":"test","uid":"nonexistent"}`,
			wantErr: true,
		},
		{
			name:    "unknown instance",
			params:  `{"instance":"invalid","uid":"sec-1"}`,
			wantErr: true,
		},
		{
			name:    "missing uid",
			params:  `{"instance":"test"}`,
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
	tool, err := NewDataSourceDescribeTool(ctx, Configs{"test": {URL: server.URL}})
	assert.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance":"test","uid":"nonexistent"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
