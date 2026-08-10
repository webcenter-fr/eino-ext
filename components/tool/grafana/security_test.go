package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestDashboardBuildBlocksNewDashboardWithProtectedTitle is an end-to-end test
// that creating a NEW dashboard (no uid) whose title matches a protected prefix
// is rejected, closing the previous protection bypass.
func TestDashboardBuildBlocksNewDashboardWithProtectedTitle(t *testing.T) {
	ctx := context.Background()
	buildTool, err := NewDashboardBuildTool(ctx, Configs{
		"t": {
			URL: "http://localhost",
			ProtectedDashboards: ProtectedDashboardsConfig{
				TitlePrefixes: []string{"Kubernetes "},
			},
		},
	})
	assert.NoError(t, err)

	_, err = buildTool.Invoke(ctx, &DashboardBuildParams{
		Instance:  "t",
		Dashboard: `{"title": "Kubernetes Evil"}`,
		Confirmed: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "protected blocklist")
}
