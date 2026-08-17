package alertmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	ctx := context.Background()

	t.Run("missing address fails validation", func(t *testing.T) {
		_, err := NewClient(ctx, Config{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Alertmanager config")
	})

	t.Run("invalid scheme rejected", func(t *testing.T) {
		_, err := NewClient(ctx, Config{Address: "ftp://am:9093"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must include scheme")
	})

	t.Run("valid config applies default timeout", func(t *testing.T) {
		c, err := NewClient(ctx, Config{Address: "http://localhost:9093"})
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, c.timeout)
	})

	t.Run("valid config with custom timeout", func(t *testing.T) {
		c, err := NewClient(ctx, Config{Address: "https://am.example.com", Timeout: "5s"})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, c.timeout)
	})

	t.Run("invalid timeout rejected", func(t *testing.T) {
		_, err := NewClient(ctx, Config{Address: "http://am.example.com", Timeout: "bogus"})
		assert.Error(t, err)
	})

	t.Run("trailing slash trimmed", func(t *testing.T) {
		c, err := NewClient(ctx, Config{Address: "http://localhost:9093/"})
		require.NoError(t, err)
		assert.NotNil(t, c)
	})
}

func TestBuildClients(t *testing.T) {
	configs := Configs{
		"with-am": {Address: "http://am:9093"},
		"no-am":   {Address: "http://am2:9093"},
	}

	clients, err := BuildClients(context.Background(), configs)
	require.NoError(t, err)
	assert.Len(t, clients, 2)
	assert.Contains(t, clients, "with-am")
	assert.Contains(t, clients, "no-am")

	t.Run("empty configs", func(t *testing.T) {
		clients, err := BuildClients(context.Background(), Configs{})
		require.NoError(t, err)
		assert.Empty(t, clients)
	})

	t.Run("invalid config wraps instance name", func(t *testing.T) {
		_, err := BuildClients(context.Background(), Configs{
			"bad": {Address: ""},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "instance bad")
	})
}

func TestListAlerts(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		gotQuery = r.URL.Query()
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

	c, err := NewClient(context.Background(), Config{Address: server.URL})
	require.NoError(t, err)

	alerts, err := c.ListAlerts(context.Background(), &listAlertsParams{
		Active: boolPtr(true),
		Filter: []string{`alertname="HighCPU"`},
	})
	require.NoError(t, err)
	assert.Len(t, alerts, 1)
	assert.NotNil(t, alerts[0].Fingerprint)
	assert.Equal(t, "abc123", *alerts[0].Fingerprint)
	assert.Equal(t, "HighCPU", alerts[0].Labels["alertname"])
	assert.Equal(t, "true", gotQuery.Get("active"))
	assert.Equal(t, []string{`alertname="HighCPU"`}, gotQuery["filter"])
}

func TestListAlertsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`"boom"`))
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), Config{Address: server.URL})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list Alertmanager alerts")
	assert.Contains(t, err.Error(), "getAlertsInternalServerError")

	var target *alert.GetAlertsInternalServerError
	assert.True(t, errors.As(err, &target))
	assert.Equal(t, "boom", target.Payload)
}

func TestAuthHeaders(t *testing.T) {
	t.Run("bearer token", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), Config{
			Address:     server.URL,
			BearerToken: "token-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})

	t.Run("basic auth", func(t *testing.T) {
		var gotUser, gotPass string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, _ = r.BasicAuth()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), Config{
			Address:  server.URL,
			Username: "admin",
			Password: "pw-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "admin", gotUser)
		assert.Equal(t, "pw-123", gotPass)
	})

	t.Run("bearer wins over basic", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), Config{
			Address:     server.URL,
			Username:    "admin",
			Password:    "pw-123",
			BearerToken: "token-123",
		})
		require.NoError(t, err)
		_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})
}

func TestSecretRedactionInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`"internal"`))
	}))
	defer server.Close()

	const (
		bearer = "LEAK-BEARER-TOKEN"
		passwd = "LEAK-PASSWORD"
		user   = "LEAK-USERNAME"
	)

	c, err := NewClient(context.Background(), Config{
		Address:     server.URL,
		Username:    user,
		Password:    passwd,
		BearerToken: bearer,
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), bearer)
	assert.NotContains(t, err.Error(), passwd)
	assert.NotContains(t, err.Error(), user)
}

func TestNewClientRejectsNonHTTPSchemes(t *testing.T) {
	for _, addr := range []string{
		"ftp://alertmanager:9093",
		"gopher://alertmanager:9093",
		"file:///etc/passwd",
		"alertmanager:9093",
	} {
		_, err := NewClient(context.Background(), Config{Address: addr})
		assert.Error(t, err, "address %q must be rejected", addr)
		assert.Contains(t, err.Error(), "must include scheme")
	}
}

// TestSecretRedactionFromEchoedHeader verifies that a server (or proxy) that
// echoes the Authorization header in its error response cannot leak
// credentials into the error message returned to the LLM. The error body is
// redacted against the bearer token, password, username, and the base64-encoded
// basic-auth pair before being surfaced (CWE-532 / CWE-200).
func TestSecretRedactionFromEchoedHeader(t *testing.T) {
	const (
		bearer = "LEAK-BEARER-TOKEN"
		passwd = "LEAK-PASSWORD"
		user   = "LEAK-USERNAME"
	)

	t.Run("bearer token echoed in error body is redacted", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			// Server echoes the Authorization header verbatim.
			_, _ = w.Write([]byte(`"auth header was: ` + r.Header.Get("Authorization") + `"`))
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), Config{
			Address:     server.URL,
			BearerToken: bearer,
		})
		require.NoError(t, err)

		_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), bearer)
		assert.NotContains(t, err.Error(), "Bearer "+bearer)
		assert.Contains(t, err.Error(), "[REDACTED]")
	})

	t.Run("basic auth echoed in error body is redacted", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`"got header: ` + r.Header.Get("Authorization") + `"`))
		}))
		defer server.Close()

		c, err := NewClient(context.Background(), Config{
			Address:  server.URL,
			Username: user,
			Password: passwd,
		})
		require.NoError(t, err)

		_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), passwd)
		assert.NotContains(t, err.Error(), user)
		// The base64-encoded "user:pass" must not appear either.
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + passwd))
		assert.NotContains(t, err.Error(), encoded)
		assert.NotContains(t, err.Error(), "Basic "+encoded)
		assert.Contains(t, err.Error(), "[REDACTED]")
	})
}

// TestRedactionTruncation verifies that non-2xx error payloads are truncated to
// amMaxErrorBodyLen before being surfaced.
func TestRedactionTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`"` + strings.Repeat("x", 2048) + `"`))
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), Config{Address: server.URL})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
	assert.Error(t, err)

	var target *alert.GetAlertsInternalServerError
	require.True(t, errors.As(err, &target))
	assert.LessOrEqual(t, len(target.Payload), amMaxErrorBodyLen+len("...(truncated)"))
	assert.True(t, strings.HasSuffix(target.Payload, "...(truncated)"))
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

// TestSecretRedactionDefaultCaseAPIError verifies that a non-2xx response
// whose status code is outside the typed 400/500 set (e.g. 403) — which the
// go-openapi runtime returns as *runtime.APIError — does not leak the
// configured credentials. For go-openapi v0.33.0 the APIError.Error() renders
// the response struct as "{}" (unexported field), so the body is not leaked;
// the redactingTransport additionally type-switches on *runtime.APIError as
// defense-in-depth so a future go-openapi change that surfaces the body
// remains safe (CWE-532 / CWE-200).
func TestSecretRedactionDefaultCaseAPIError(t *testing.T) {
	const bearer = "LEAK-BEARER-TOKEN"

	for _, code := range []int{
		http.StatusUnauthorized,       // 401
		http.StatusForbidden,          // 403
		http.StatusNotFound,           // 404
		http.StatusTooManyRequests,    // 429
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout,     // 504
	} {
		t.Run(fmt.Sprintf("code=%d", code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				// Server echoes the Authorization header verbatim in the body.
				_, _ = w.Write([]byte(`"echoed: ` + r.Header.Get("Authorization") + `"`))
			}))
			defer server.Close()

			c, err := NewClient(context.Background(), Config{
				Address:     server.URL,
				BearerToken: bearer,
			})
			require.NoError(t, err)

			_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
			require.Error(t, err)
			// The configured bearer token must never appear in the surfaced
			// error, regardless of which status code path produced it.
			assert.NotContains(t, err.Error(), bearer)
			assert.NotContains(t, err.Error(), "Bearer "+bearer)
		})
	}
}

// TestNetworkErrorDoesNotLeakSecrets verifies that a transport-level error
// (e.g. connection refused) surfaces a *url.Error whose URL does not contain
// credentials. The Authorization header is sent in the HTTP headers, not the
// URL, so a network failure cannot leak it via the error string
// (CWE-532 / CWE-200).
func TestNetworkErrorDoesNotLeakSecrets(t *testing.T) {
	const bearer = "LEAK-BEARER-TOKEN"

	// A server that is already closed → connection refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	c, err := NewClient(context.Background(), Config{
		Address:     "http://" + addr,
		BearerToken: bearer,
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
	require.Error(t, err)
	// The *url.Error contains the request URL (no userinfo, no auth header).
	assert.NotContains(t, err.Error(), bearer)
	assert.NotContains(t, err.Error(), "Bearer "+bearer)
}

// TestRedirectStripsAuthorization verifies that the configured HTTP client
// strips the Authorization header when following a redirect, so a
// compromised or misconfigured server cannot redirect the credentialed
// request to a different endpoint and harvest the Bearer/Basic credentials
// (CWE-200). The Alertmanager v2 API does not redirect, so this is purely
// defense-in-depth.
func TestRedirectStripsAuthorization(t *testing.T) {
	const bearer = "LEAK-BEARER-TOKEN"

	var targetAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect the credentialed request to the target server.
		http.Redirect(w, r, targetURL.String()+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	c, err := NewClient(context.Background(), Config{
		Address:     redirector.URL,
		BearerToken: bearer,
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(context.Background(), &listAlertsParams{})
	require.NoError(t, err)
	// The Authorization header must NOT be forwarded to the redirect target.
	assert.Empty(t, targetAuth, "Authorization header must be stripped on redirect")
}
