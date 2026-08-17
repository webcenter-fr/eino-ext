package prometheus

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertmanagerClient(t *testing.T) {
	t.Run("missing address fails validation", func(t *testing.T) {
		_, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Alertmanager config")
	})

	t.Run("invalid scheme rejected", func(t *testing.T) {
		_, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: "ftp://alertmanager:9093"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must include scheme")
	})

	t.Run("valid config applies default timeout", func(t *testing.T) {
		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: "http://localhost:9093"})
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:9093", c.baseURL)
		assert.Equal(t, 30*time.Second, c.timeout)
	})

	t.Run("valid config with custom timeout", func(t *testing.T) {
		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: "https://am.example.com", Timeout: "5s"})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, c.timeout)
	})

	t.Run("invalid timeout rejected", func(t *testing.T) {
		_, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: "http://am.example.com", Timeout: "bogus"})
		assert.Error(t, err)
	})

	t.Run("trailing slash trimmed", func(t *testing.T) {
		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: "http://localhost:9093/"})
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:9093", c.baseURL)
	})
}

func TestBuildAlertmanagerClients(t *testing.T) {
	configs := Configs{
		"with-am": {Address: "http://prom:9090", Alertmanager: &AlertmanagerConfig{Address: "http://am:9093"}},
		"no-am":   {Address: "http://prom:9090"},
	}

	clients, err := BuildAlertmanagerClients(context.Background(), configs)
	require.NoError(t, err)
	assert.Len(t, clients, 1)
	assert.Contains(t, clients, "with-am")
	assert.NotContains(t, clients, "no-am")

	t.Run("empty configs", func(t *testing.T) {
		clients, err := BuildAlertmanagerClients(context.Background(), Configs{})
		require.NoError(t, err)
		assert.Empty(t, clients)
	})

	t.Run("invalid alertmanager config wraps instance name", func(t *testing.T) {
		_, err := BuildAlertmanagerClients(context.Background(), Configs{
			"bad": {Address: "http://prom:9090", Alertmanager: &AlertmanagerConfig{Address: ""}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "instance bad")
	})
}

func TestAlertmanagerDoRequestErrorWrapping(t *testing.T) {
	newClient := func(status int, body string) (*alertmanagerClient, *httptest.Server) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return &alertmanagerClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: 5 * time.Second}, server
	}

	t.Run("400 produces amHTTPError", func(t *testing.T) {
		c, _ := newClient(http.StatusBadRequest, `{"error":"bad request"}`)
		_, status, err := c.doRequest(context.Background(), http.MethodGet, "/api/v2/alerts", nil)
		assert.Error(t, err)
		assert.Equal(t, 400, status)
		assert.True(t, isAMStatus(err, 400))
		assert.Contains(t, err.Error(), "Alertmanager API error")
	})

	t.Run("500 produces amHTTPError", func(t *testing.T) {
		c, _ := newClient(http.StatusInternalServerError, `{"error":"boom"}`)
		_, status, err := c.doRequest(context.Background(), http.MethodPost, "/api/v2/alerts", strings.NewReader("[]"))
		assert.Error(t, err)
		assert.Equal(t, 500, status)
		assert.True(t, isAMStatus(err, 500))
		assert.False(t, isAMStatus(err, 400))
	})

	t.Run("large error body truncated", func(t *testing.T) {
		c, _ := newClient(http.StatusInternalServerError, strings.Repeat("x", 2048))
		_, _, err := c.doRequest(context.Background(), http.MethodGet, "/api/v2/alerts", nil)
		assert.Error(t, err)
		var he *amHTTPError
		require.True(t, errors.As(err, &he))
		assert.LessOrEqual(t, len(he.body), amMaxErrorBodyLen+len("...(truncated)"))
		assert.True(t, strings.HasSuffix(he.body, "...(truncated)"))
	})
}

func TestAlertmanagerListAlerts(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"labels":{"alertname":"HighCPU"},
			"annotations":{"summary":"high cpu"},
			"generatorURL":"http://gen",
			"startsAt":"2026-08-17T10:00:00Z",
			"updatedAt":"2026-08-17T10:01:00Z",
			"endsAt":"2026-08-17T10:30:00Z",
			"fingerprint":"abc123",
			"receivers":[{"name":"slack"}],
			"status":{"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
		}]`))
	}))
	defer server.Close()

	c := &alertmanagerClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: 5 * time.Second}

	alerts, err := c.ListAlerts(context.Background(), &amListAlertsParams{
		Active: boolPtr(true),
		Filter: []string{`alertname="HighCPU"`},
	})
	require.NoError(t, err)
	assert.Len(t, alerts, 1)
	assert.Equal(t, "abc123", alerts[0].Fingerprint)
	assert.Equal(t, model.LabelValue("HighCPU"), alerts[0].Labels["alertname"])
	assert.Equal(t, "active", alerts[0].Status.State)
	assert.Contains(t, gotQuery, "active=true")
	assert.Contains(t, gotQuery, "filter=alertname%3D%22HighCPU%22")
}

func TestAlertmanagerListAlertsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	c := &alertmanagerClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: 5 * time.Second}
	_, err := c.ListAlerts(context.Background(), &amListAlertsParams{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list Alertmanager alerts")
}

func TestAlertmanagerPostAlerts(t *testing.T) {
	var gotMethod string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	c := &alertmanagerClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: 5 * time.Second}

	alert := postableAlert{
		Labels:      model.LabelSet{"alertname": "HighCPU"},
		Annotations: model.LabelSet{"summary": "high cpu"},
	}
	err := c.PostAlerts(context.Background(), []postableAlert{alert})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	assert.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("HighCPU"), posted[0].Labels["alertname"])
	assert.Equal(t, model.LabelValue("high cpu"), posted[0].Annotations["summary"])
}

func TestAlertmanagerPostAlertsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer server.Close()

	c := &alertmanagerClient{baseURL: server.URL, httpClient: &http.Client{}, timeout: 5 * time.Second}
	err := c.PostAlerts(context.Background(), []postableAlert{{Labels: model.LabelSet{"alertname": "X"}}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to post alerts to Alertmanager")
}

func TestAlertmanagerAuthHeaders(t *testing.T) {
	t.Run("bearer token", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:     server.URL,
			BearerToken: "token-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})

	t.Run("basic auth", func(t *testing.T) {
		var gotUser, gotPass string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, _ = r.BasicAuth()
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:  server.URL,
			Username: "admin",
			Password: "pw-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "admin", gotUser)
		assert.Equal(t, "pw-123", gotPass)
	})

	t.Run("bearer wins over basic", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:     server.URL,
			Username:    "admin",
			Password:    "pw-123",
			BearerToken: "token-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})
}

func TestAlertmanagerSecretRedactionInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer server.Close()

	const (
		bearer = "LEAK-BEARER-TOKEN"
		passwd = "LEAK-PASSWORD"
		user   = "LEAK-USERNAME"
	)

	c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
		Address:     server.URL,
		Username:    user,
		Password:    passwd,
		BearerToken: bearer,
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), bearer)
	assert.NotContains(t, err.Error(), passwd)
	assert.NotContains(t, err.Error(), user)

	err = c.PostAlerts(context.Background(), []postableAlert{{Labels: model.LabelSet{"alertname": "X"}}})
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), bearer)
	assert.NotContains(t, err.Error(), passwd)
	assert.NotContains(t, err.Error(), user)
}

func TestNewAlertmanagerClientRejectsNonHTTPSchemes(t *testing.T) {
	for _, addr := range []string{
		"ftp://alertmanager:9093",
		"gopher://alertmanager:9093",
		"file:///etc/passwd",
		"alertmanager:9093",
	} {
		_, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{Address: addr})
		assert.Error(t, err, "address %q must be rejected", addr)
	}
}

// TestAlertmanagerSecretRedactionFromEchoedHeader verifies that a server (or
// proxy) that echoes the Authorization header in its error response cannot
// leak credentials into the error message returned to the LLM. The error
// body is redacted against the bearer token, password, username, and the
// base64-encoded basic-auth pair before being surfaced (CWE-532 / CWE-200).
func TestAlertmanagerSecretRedactionFromEchoedHeader(t *testing.T) {
	const (
		bearer = "LEAK-BEARER-TOKEN"
		passwd = "LEAK-PASSWORD"
		user   = "LEAK-USERNAME"
	)

	t.Run("bearer token echoed in error body is redacted", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			// Server echoes the Authorization header verbatim.
			_, _ = w.Write([]byte(`{"error":"auth header was: ` + r.Header.Get("Authorization") + `"}`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:     server.URL,
			BearerToken: bearer,
		})
		require.NoError(t, err)

		_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), bearer)
		assert.NotContains(t, err.Error(), "Bearer "+bearer)
		assert.Contains(t, err.Error(), "[REDACTED]")
	})

	t.Run("basic auth echoed in error body is redacted", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"got header: ` + r.Header.Get("Authorization") + `"}`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:  server.URL,
			Username: user,
			Password: passwd,
		})
		require.NoError(t, err)

		_, err = c.ListAlerts(context.Background(), &amListAlertsParams{})
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), passwd)
		assert.NotContains(t, err.Error(), user)
		// The base64-encoded "user:pass" must not appear either.
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + passwd))
		assert.NotContains(t, err.Error(), encoded)
		assert.NotContains(t, err.Error(), "Basic "+encoded)
		assert.Contains(t, err.Error(), "[REDACTED]")
	})

	t.Run("redaction applies to POST errors too", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"echo":"` + r.Header.Get("Authorization") + `"}`))
		}))
		defer server.Close()

		c, err := NewAlertmanagerClient(context.Background(), AlertmanagerConfig{
			Address:     server.URL,
			BearerToken: bearer,
		})
		require.NoError(t, err)

		err = c.PostAlerts(context.Background(), []postableAlert{{Labels: model.LabelSet{"alertname": "X"}}})
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), bearer)
		assert.NotContains(t, err.Error(), "Bearer "+bearer)
	})
}

// TestBuildRedactSecrets verifies the secret-redaction allowlist covers all
// forms in which credentials can appear in an echoed Authorization header.
func TestBuildRedactSecrets(t *testing.T) {
	t.Run("bearer only", func(t *testing.T) {
		s := buildRedactSecrets("", "", "tok")
		assert.Contains(t, s, "tok")
		assert.Contains(t, s, "Bearer tok")
	})

	t.Run("basic auth", func(t *testing.T) {
		s := buildRedactSecrets("admin", "pw", "")
		assert.Contains(t, s, "pw")
		assert.Contains(t, s, "admin")
		encoded := base64.StdEncoding.EncodeToString([]byte("admin:pw"))
		assert.Contains(t, s, encoded)
		assert.Contains(t, s, "Basic "+encoded)
	})

	t.Run("empty config yields no secrets", func(t *testing.T) {
		s := buildRedactSecrets("", "", "")
		assert.Empty(t, s)
	})
}
