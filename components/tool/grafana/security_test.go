package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

const testTimeout = 5 * time.Second

// TestGetDashboardPathEscape verifies that a uid containing path-traversal
// characters is URL-escaped on the wire so it cannot reach a different API
// endpoint. We check r.RequestURI (the raw request line) because r.URL.Path
// is the *decoded* form and would hide the escaping.
func TestGetDashboardPathEscape(t *testing.T) {
	var capturedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"dashboard":{},"meta":{}}`))
	}))
	defer server.Close()

	c := &grafanaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		timeout:    testTimeout,
	}

	// A uid with ".." must be escaped so it stays a single path segment on
	// the wire (the "/" becomes %2F, which routers do not treat as a path
	// separator).
	_, err := c.GetDashboard(context.Background(), "../db")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/..%2Fdb", capturedURI,
		"uid must be path-escaped on the wire to prevent endpoint traversal")

	// A uid with a raw "/" must also be escaped.
	_, err = c.GetDashboard(context.Background(), "foo/bar")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/foo%2Fbar", capturedURI)

	// A uid with "?" must be escaped so it cannot start a query string.
	_, err = c.GetDashboard(context.Background(), "foo?bar=baz")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/foo%3Fbar=baz", capturedURI)
}

// TestCheckProtected404TypedError verifies that checkProtected treats a 404 as
// "not protected" via the typed *httpError, not via fragile string matching.
func TestCheckProtected404TypedError(t *testing.T) {
	mux := http.NewServeMux()
	// /api/dashboards/uid/missing → 404
	mux.HandleFunc("/api/dashboards/uid/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	})
	// /api/dashboards/uid/broken → 500 (must NOT be treated as 404)
	mux.HandleFunc("/api/dashboards/uid/broken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"internal server error"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	b := &baseTool{
		clients: map[string]*grafanaClient{
			"t": {
				baseURL:    server.URL,
				httpClient: &http.Client{},
				timeout:    testTimeout,
			},
		},
		configs:        Configs{"t": {URL: server.URL}},
		knownInstances: []string{"t"},
		protected:      map[string]*dashboardProtection{"t": buildProtection(ProtectedDashboardsConfig{UIDs: []string{"x"}})},
	}

	// 404 → not protected, no error.
	err := b.checkProtected(context.Background(), "t", "missing")
	assert.NoError(t, err)

	// 500 → must surface as an error (not silently treated as 404).
	err = b.checkProtected(context.Background(), "t", "broken")
	assert.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "is protected"),
		"a 500 must not be misclassified as a protected-dashboard hit")
}

// TestCheckProtectedModelBlocksNewDashboardWithProtectedTitle verifies the
// defense-in-depth check that prevents creating/renaming a dashboard so that
// it matches protected criteria (title prefix here).
func TestCheckProtectedModelBlocksNewDashboardWithProtectedTitle(t *testing.T) {
	prot := buildProtection(ProtectedDashboardsConfig{
		TitlePrefixes: []string{"Kubernetes "},
	})
	b := &baseTool{
		protected: map[string]*dashboardProtection{"t": prot},
	}

	// New dashboard (no uid) with a protected title prefix must be blocked.
	err := b.checkProtectedModel("t", map[string]any{"title": "Kubernetes Monitoring"}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "protected blocklist")

	// Non-protected title must pass.
	err = b.checkProtectedModel("t", map[string]any{"title": "My App"}, "")
	assert.NoError(t, err)

	// Protected tag must be blocked.
	prot2 := buildProtection(ProtectedDashboardsConfig{Tags: []string{"infrastructure"}})
	b.protected["t"] = prot2
	err = b.checkProtectedModel("t", map[string]any{
		"title": "My App",
		"tags":  []any{"infrastructure"},
	}, "")
	assert.Error(t, err)

	// Protected folder must be blocked.
	prot3 := buildProtection(ProtectedDashboardsConfig{Folders: []string{"infra-folder"}})
	b.protected["t"] = prot3
	err = b.checkProtectedModel("t", map[string]any{"title": "My App"}, "infra-folder")
	assert.Error(t, err)

	// No protection configured → always passes.
	b.protected["t"] = nil
	err = b.checkProtectedModel("t", map[string]any{"title": "anything"}, "any-folder")
	assert.NoError(t, err)
}

// TestDashboardWriteBlocksNewDashboardWithProtectedTitle is an end-to-end test
// that creating a NEW dashboard (no uid) whose title matches a protected prefix
// is rejected, closing the previous protection bypass.
func TestDashboardWriteBlocksNewDashboardWithProtectedTitle(t *testing.T) {
	ctx := context.Background()
	writeTool, err := NewDashboardWriteTool(ctx, Configs{
		"t": {
			URL: "http://localhost",
			ProtectedDashboards: ProtectedDashboardsConfig{
				TitlePrefixes: []string{"Kubernetes "},
			},
		},
	})
	assert.NoError(t, err)

	_, err = writeTool.Invoke(ctx, &DashboardWriteParams{
		Instance:  "t",
		Operation: "create",
		Dashboard: `{"title": "Kubernetes Evil"}`,
		Confirmed: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "protected blocklist")
}

// TestDeleteDashboardPathEscape verifies that a uid containing path-traversal
// characters is URL-escaped on the wire so it cannot reach a different API
// endpoint. It mirrors TestGetDashboardPathEscape for the DELETE endpoint.
func TestDeleteDashboardPathEscape(t *testing.T) {
	var capturedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"title":"x","message":"deleted","id":1}`))
	}))
	defer server.Close()

	c := &grafanaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		timeout:    testTimeout,
	}

	_, err := c.DeleteDashboard(context.Background(), "../db")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/..%2Fdb", capturedURI,
		"uid must be path-escaped on the wire to prevent endpoint traversal")

	_, err = c.DeleteDashboard(context.Background(), "foo/bar")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/foo%2Fbar", capturedURI)

	_, err = c.DeleteDashboard(context.Background(), "foo?bar=baz")
	assert.NoError(t, err)
	assert.Equal(t, "/api/dashboards/uid/foo%3Fbar=baz", capturedURI)
}

// TestGetDataSourcePathEscape verifies that a uid containing path-traversal
// characters is URL-escaped on the wire so it cannot reach a different API
// endpoint. It mirrors TestGetDashboardPathEscape for the data source endpoint.
func TestGetDataSourcePathEscape(t *testing.T) {
	var capturedURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := &grafanaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		timeout:    testTimeout,
	}

	_, err := c.GetDataSource(context.Background(), "../ds")
	assert.NoError(t, err)
	assert.Equal(t, "/api/datasources/uid/..%2Fds", capturedURI,
		"uid must be path-escaped on the wire to prevent endpoint traversal")

	_, err = c.GetDataSource(context.Background(), "foo/bar")
	assert.NoError(t, err)
	assert.Equal(t, "/api/datasources/uid/foo%2Fbar", capturedURI)

	_, err = c.GetDataSource(context.Background(), "foo?bar=baz")
	assert.NoError(t, err)
	assert.Equal(t, "/api/datasources/uid/foo%3Fbar=baz", capturedURI)
}

// TestRedactSensitiveJSON is a table-driven test for the recursive redactor.
func TestRedactSensitiveJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "top level sensitive fields",
			in:   `{"password":"x","token":"y","apiKey":"z","httpHeaderValue":"h"}`,
			want: `{"password":"<redacted>","token":"<redacted>","apiKey":"<redacted>","httpHeaderValue":"<redacted>"}`,
		},
		{
			name: "exact auth redacted, name kept",
			in:   `{"auth":"x","name":"prometheus"}`,
			want: `{"auth":"<redacted>","name":"prometheus"}`,
		},
		{
			name: "nested map",
			in:   `{"jsonData":{"clientSecret":"s","timeField":"@timestamp"}}`,
			want: `{"jsonData":{"clientSecret":"<redacted>","timeField":"@timestamp"}}`,
		},
		{
			name: "slice of maps",
			in:   `[{"accessToken":"a"},{"name":"n"}]`,
			want: `[{"accessToken":"<redacted>"},{"name":"n"}]`,
		},
		{
			name: "case insensitivity",
			in:   `{"APIKey":"a","PassWord":"p"}`,
			want: `{"APIKey":"<redacted>","PassWord":"<redacted>"}`,
		},
		{
			name: "substring safety authMode",
			in:   `{"authMode":"oidc"}`,
			want: `{"authMode":"oidc"}`,
		},
		// ── Adversarial / regression cases for previously under-redacted keys ──
		{
			name: "aws accessKey redacted",
			in:   `{"accessKey":"AKIAEXAMPLE","accessKeyId":"AKIAEXAMPLE2"}`,
			want: `{"accessKey":"<redacted>","accessKeyId":"<redacted>"}`,
		},
		{
			name: "aws sigV4 keys redacted",
			in:   `{"sigV4AccessKey":"AKIAEXAMPLE","sigV4SecretKey":"SHHH","sigV4Region":"us-east-1"}`,
			want: `{"sigV4AccessKey":"<redacted>","sigV4SecretKey":"<redacted>","sigV4Region":"us-east-1"}`,
		},
		{
			name: "aws secretAccessKey redacted",
			in:   `{"secretAccessKey":"SHHH"}`,
			want: `{"secretAccessKey":"<redacted>"}`,
		},
		{
			name: "numbered httpHeaderValue redacted",
			in:   `{"httpHeaderValue1":"h1","httpHeaderValue2":"h2"}`,
			want: `{"httpHeaderValue1":"<redacted>","httpHeaderValue2":"<redacted>"}`,
		},
		{
			name: "customHttpHeaders redacted wholesale",
			in:   `{"customHttpHeaders":{"X-Api-Key":"k","Authorization":"a","X-Forwarded-For":"f"}}`,
			want: `{"customHttpHeaders":"<redacted>"}`,
		},
		{
			name: "hyphenated X-Api-Key redacted",
			in:   `{"X-Api-Key":"k","api-key":"k2"}`,
			want: `{"X-Api-Key":"<redacted>","api-key":"<redacted>"}`,
		},
		{
			name: "underscore api_key redacted",
			in:   `{"api_key":"k","client_secret":"s"}`,
			want: `{"api_key":"<redacted>","client_secret":"<redacted>"}`,
		},
		{
			name: "mixed case access key redacted",
			in:   `{"ACCESSKEY":"AKIA","AccessKeyId":"AKIA2"}`,
			want: `{"ACCESSKEY":"<redacted>","AccessKeyId":"<redacted>"}`,
		},
		{
			name: "benign dashboard keys preserved",
			in:   `{"timeField":"@timestamp","database":"mydb","region":"us-east-1","maxLines":1000,"timeInterval":"15s","authMode":"oidc","oauthPassThru":true}`,
			want: `{"timeField":"@timestamp","database":"mydb","region":"us-east-1","maxLines":1000,"timeInterval":"15s","authMode":"oidc","oauthPassThru":true}`,
		},
		{
			name: "nested object with sensitive key",
			in:   `{"jsonData":{"sigV4":{"accessKey":"AKIA","secretKey":"SHHH"},"timeField":"@timestamp"}}`,
			want: `{"jsonData":{"sigV4":{"accessKey":"<redacted>","secretKey":"<redacted>"},"timeField":"@timestamp"}}`,
		},
		{
			name: "array of objects with secrets",
			in:   `{"items":[{"apiKey":"a"},{"accessKey":"b"},{"name":"n"}]}`,
			want: `{"items":[{"apiKey":"<redacted>"},{"accessKey":"<redacted>"},{"name":"n"}]}`,
		},
		{
			name: "non-string sensitive values redacted",
			in:   `{"apiKey":12345,"accessKey":true,"token":null}`,
			want: `{"apiKey":"<redacted>","accessKey":"<redacted>","token":"<redacted>"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in any
			assert.NoError(t, json.Unmarshal([]byte(tt.in), &in))

			got := redactSensitiveJSON(in)

			gotJSON, err := json.Marshal(got)
			assert.NoError(t, err)
			assert.JSONEq(t, tt.want, string(gotJSON))
		})
	}

	t.Run("nil map input", func(t *testing.T) {
		assert.Nil(t, redactedJSONData(nil))
	})
}

// TestRedactNoSecretValueLeaks is a defense-in-depth check: for a broad set of
// secret-bearing keys, the redacted output must never contain the original
// secret value, regardless of casing, separators, or nesting. This guards
// against future regressions in the matcher.
func TestRedactNoSecretValueLeaks(t *testing.T) {
	// Each value is a unique sentinel; none may appear in the redacted output.
	secretKeys := []string{
		"password", "basicAuthPassword", "secret", "clientSecret", "secretAccessKey",
		"token", "accessToken", "refreshToken", "authToken", "privateKey",
		"apiKey", "APIKey", "api_key", "api-key", "X-Api-Key",
		"accessKey", "accessKeyId", "ACCESSKEY", "AccessKeyId",
		"sigV4AccessKey", "sigV4SecretKey",
		"httpHeaderValue", "httpHeaderValue1", "httpHeaderValue2",
		"customHttpHeaders", "credential", "authorization", "bearer",
		"auth", "pass", "pwd",
	}
	for _, k := range secretKeys {
		sentinel := "LEAK-" + k
		in := map[string]any{k: sentinel}
		got := redactedJSONData(in)
		b, err := json.Marshal(got)
		assert.NoError(t, err)
		assert.NotContains(t, string(b), sentinel,
			"secret value for key %q leaked through redaction: %s", k, string(b))
		// JSON marshalling escapes "<redacted>" to "\u003credacted\u003e".
		assert.Contains(t, string(b), `\u003credacted\u003e`,
			"key %q was not redacted to <redacted>: %s", k, string(b))
	}
}

// TestDataSourceDescribeRedactsAWSAndHeaderSecrets verifies end-to-end that a
// describe response carrying AWS SigV4 credentials and custom HTTP headers
// (a common Grafana cloud-datasource configuration) redacts every secret and
// does not leak the bearer token, access key, secret key, or header values.
func TestDataSourceDescribeRedactsAWSAndHeaderSecrets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources/uid/aws-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 7, "uid": "aws-1", "orgId": 1, "name": "CloudWatch",
			"type": "cloudwatch", "access": "proxy", "url": "http://cw:3100",
			"isDefault": false, "readOnly": true, "version": 2,
			"jsonData": {
				"sigV4AccessKey": "AKIA-LEAK",
				"sigV4SecretKey": "SHHH-LEAK",
				"sigV4Region": "us-east-1",
				"accessKey": "AKIA-LEAK2",
				"secretAccessKey": "SHHH-LEAK2",
				"customHttpHeaders": {"X-Api-Key": "hdr-LEAK", "Authorization": "Bearer hdr-LEAK2"},
				"httpHeaderValue1": "hdr-LEAK3",
				"apiKey": "ak-LEAK",
				"region": "us-east-1",
				"timeField": "@timestamp"
			},
			"secureJsonFields": {"sigV4SecretKey": true},
			"password": "pw-LEAK", "basicAuthPassword": "ba-LEAK"
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	tool, err := NewDataSourceTool(ctx, Configs{"t": {URL: server.URL}})
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance":"t","uid":"aws-1"}`)
	assert.NoError(t, err)

	// No secret value may appear in the output.
	for _, leak := range []string{
		"AKIA-LEAK", "AKIA-LEAK2", "SHHH-LEAK", "SHHH-LEAK2",
		"hdr-LEAK", "hdr-LEAK2", "hdr-LEAK3", "ak-LEAK",
		"pw-LEAK", "ba-LEAK",
	} {
		assert.NotContains(t, result, leak, "secret value %q leaked into describe output", leak)
	}
	// Top-level secret-bearing fields are excluded entirely.
	for _, field := range []string{`"password"`, `"basicAuthPassword"`, `"secureJsonFields"`} {
		assert.NotContains(t, result, field, "excluded field %q present in output", field)
	}
	// Benign config needed for dashboard building is preserved.
	assert.Contains(t, result, `"sigV4Region":"us-east-1"`)
	assert.Contains(t, result, `"region":"us-east-1"`)
	assert.Contains(t, result, `"timeField":"@timestamp"`)
	// Sensitive keys are present but redacted.
	assert.Contains(t, result, `"sigV4AccessKey":"\u003credacted\u003e"`)
	assert.Contains(t, result, `"sigV4SecretKey":"\u003credacted\u003e"`)
	assert.Contains(t, result, `"customHttpHeaders":"\u003credacted\u003e"`)
	assert.Contains(t, result, `"httpHeaderValue1":"\u003credacted\u003e"`)
	assert.Contains(t, result, `"apiKey":"\u003credacted\u003e"`)
}

// TestDoRequestResponseBodyCap verifies that a successful response body larger
// than maxResponseBodyLen is rejected with an error instead of being read into
// memory unbounded.
func TestDoRequestResponseBodyCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, maxResponseBodyLen+1))
	}))
	defer server.Close()

	c := &grafanaClient{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		timeout:    testTimeout,
	}

	_, _, err := c.doRequest(context.Background(), http.MethodGet, "/large", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "response body exceeds")
}

// TestDataSourceDescribeExcludesSecrets verifies end-to-end that a describe
// response containing secrets produces output with those secrets excluded or
// redacted.
func TestDataSourceDescribeExcludesSecrets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources/uid/sec-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 1, "uid": "sec-1", "orgId": 1, "name": "Secure DS",
			"type": "prometheus", "access": "proxy", "url": "http://prom:9090",
			"user": "admin", "database": "", "basicAuth": true,
			"basicAuthUser": "svc", "isDefault": true,
			"jsonData": {"httpHeaderValue": "sup3r-secret", "timeField": "@timestamp"},
			"secureJsonFields": {"httpHeaderValue": true},
			"readOnly": false, "version": 1,
			"password": "top-secret", "basicAuthPassword": "also-secret"
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	tool, err := NewDataSourceTool(ctx, Configs{
		"t": {URL: server.URL},
	})
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, `{"instance":"t","uid":"sec-1"}`)
	assert.NoError(t, err)

	assert.NotContains(t, result, `"password"`)
	assert.NotContains(t, result, `"basicAuthPassword"`)
	assert.NotContains(t, result, `"secureJsonFields"`)
	assert.NotContains(t, result, "top-secret")
	assert.NotContains(t, result, "also-secret")
	assert.NotContains(t, result, "sup3r-secret")
	assert.Contains(t, result, `\u003credacted\u003e`)
	assert.Contains(t, result, `"timeField":"@timestamp"`)
}
