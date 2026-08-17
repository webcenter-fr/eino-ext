package prometheus

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// alertmanagerClient is a minimal HTTP client for the Alertmanager v2 API.
type alertmanagerClient struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	// redactSecrets holds the non-empty secret values (bearer token, password,
	// username, and their base64-encoded basic-auth forms) that must never
	// appear in error messages. doRequest strips these from non-2xx response
	// bodies before including a truncated snippet in amHTTPError, so a server
	// (or proxy) that echoes the Authorization header cannot leak credentials
	// into tool outputs or LLM-visible error strings.
	redactSecrets []string
}

// NewAlertmanagerClient creates an Alertmanager HTTP client from config.
// Reuses authRoundTripper for Basic/Bearer auth and TLS skip verify.
func NewAlertmanagerClient(ctx context.Context, cfg AlertmanagerConfig) (*alertmanagerClient, error) {
	if cfg.Timeout == "" {
		cfg.Timeout = "30s"
	}

	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid Alertmanager config")
	}

	if !strings.HasPrefix(cfg.Address, "http://") && !strings.HasPrefix(cfg.Address, "https://") {
		return nil, errors.Errorf("Alertmanager address must include scheme (http:// or https://): %s", cfg.Address)
	}

	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid Alertmanager timeout value: %s", cfg.Timeout)
	}

	var rt http.RoundTripper
	if cfg.TLSSkipVerify {
		rt = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	} else {
		rt = http.DefaultTransport
	}

	rt = &authRoundTripper{
		rt:          rt,
		username:    cfg.Username,
		password:    cfg.Password,
		bearerToken: cfg.BearerToken,
	}

	return &alertmanagerClient{
		baseURL:       strings.TrimRight(cfg.Address, "/"),
		httpClient:    &http.Client{Transport: rt, Timeout: timeout},
		timeout:       timeout,
		redactSecrets: buildRedactSecrets(cfg.Username, cfg.Password, cfg.BearerToken),
	}, nil
}

// buildRedactSecrets returns the list of secret-bearing strings that must be
// stripped from non-2xx response bodies before they are surfaced in error
// messages. It covers the raw bearer token, the raw password, the raw
// username, and — when basic auth is configured — the base64-encoded
// "username:password" pair and its "Basic <encoded>" header form. This
// defends against a server or proxy that echoes request headers in error
// responses (CWE-532 / CWE-200).
func buildRedactSecrets(username, password, bearerToken string) []string {
	secrets := make([]string, 0, 6)
	if bearerToken != "" {
		secrets = append(secrets, bearerToken, "Bearer "+bearerToken)
	}
	if password != "" {
		secrets = append(secrets, password)
	}
	if username != "" {
		secrets = append(secrets, username)
	}
	if username != "" || password != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		secrets = append(secrets, encoded, "Basic "+encoded)
	}
	return secrets
}

// redactSecret replaces every occurrence of each configured secret value in s
// with "[REDACTED]". It is applied to non-2xx response bodies before they are
// truncated and included in amHTTPError, so credentials sent in the
// Authorization header cannot leak back through error messages even if the
// server echoes the header.
func redactSecret(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

// BuildAlertmanagerClients builds clients for every instance whose Config
// has a non-nil Alertmanager field. Instances without Alertmanager config are
// skipped (not an error).
func BuildAlertmanagerClients(ctx context.Context, configs Configs) (map[string]*alertmanagerClient, error) {
	clients := make(map[string]*alertmanagerClient)
	for instanceName, config := range configs {
		if config.Alertmanager == nil {
			continue
		}
		client, err := NewAlertmanagerClient(ctx, *config.Alertmanager)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create Alertmanager client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}
	return clients, nil
}

// amMaxErrorBodyLen caps how much of a non-2xx response body is included in the
// returned error message. This prevents large or sensitive Alertmanager error
// bodies from being leaked verbatim through error chains.
const amMaxErrorBodyLen = 512

// amHTTPError is a typed error carrying the HTTP status code so callers can
// branch on it (e.g. 404 → not found) without parsing the error string.
type amHTTPError struct {
	statusCode int
	method     string
	path       string
	body       string
}

func (e *amHTTPError) Error() string {
	return fmt.Sprintf("Alertmanager API error: HTTP %d %s %s: %s", e.statusCode, e.method, e.path, e.body)
}

// isAMStatus returns true if err is an *amHTTPError with the given status.
func isAMStatus(err error, code int) bool {
	var he *amHTTPError
	if errors.As(err, &he) {
		return he.statusCode == code
	}
	return false
}

// doRequest executes an HTTP request and returns the raw body.
// Returns an error for status >= 400. The error is an *amHTTPError carrying the
// status code; its message includes a truncated response body to avoid leaking
// large or sensitive Alertmanager error payloads.
func (c *alertmanagerClient) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(reqCtx, method, fullURL, body)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed to create request %s %s", method, path)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "request failed for %s %s", method, path)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, errors.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode >= 400 {
		snippet := redactSecret(string(respBody), c.redactSecrets)
		if len(snippet) > amMaxErrorBodyLen {
			snippet = snippet[:amMaxErrorBodyLen] + "...(truncated)"
		}
		return nil, resp.StatusCode, &amHTTPError{
			statusCode: resp.StatusCode,
			method:     method,
			path:       path,
			body:       snippet,
		}
	}

	return respBody, resp.StatusCode, nil
}

// boolPtr returns a pointer to the given bool. It is used to distinguish
// "unset" (nil) from an explicit true/false in optional Alertmanager query
// parameters.
func boolPtr(b bool) *bool {
	return &b
}

// toLabelSet converts a map[string]string to a model.LabelSet. A nil input
// returns nil so that `omitempty` wire fields are omitted rather than rendered
// as an empty object.
func toLabelSet(m map[string]string) model.LabelSet {
	if m == nil {
		return nil
	}
	ls := make(model.LabelSet, len(m))
	for k, v := range m {
		ls[model.LabelName(k)] = model.LabelValue(v)
	}
	return ls
}

// ─── Wire types ──────────────────────────────────────────────────────────

// postableAlert is the body of POST /api/v2/alerts.
type postableAlert struct {
	Labels       model.LabelSet `json:"labels"`
	Annotations  model.LabelSet `json:"annotations,omitempty"`
	StartsAt     *time.Time     `json:"startsAt,omitempty"`
	EndsAt       *time.Time     `json:"endsAt,omitempty"`
	GeneratorURL string         `json:"generatorURL,omitempty"`
}

// gettableAlert is a single element of the GET /api/v2/alerts response array.
type gettableAlert struct {
	Labels       model.LabelSet `json:"labels"`
	Annotations  model.LabelSet `json:"annotations"`
	GeneratorURL string         `json:"generatorURL"`
	StartsAt     time.Time      `json:"startsAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	EndsAt       time.Time      `json:"endsAt"`
	Fingerprint  string         `json:"fingerprint"`
	Receivers    []amReceiver   `json:"receivers"`
	Status       amAlertStatus  `json:"status"`
}

type amReceiver struct {
	Name string `json:"name"`
}

type amAlertStatus struct {
	State       string   `json:"state"`
	SilencedBy  []string `json:"silencedBy"`
	InhibitedBy []string `json:"inhibitedBy"`
	MutedBy     []string `json:"mutedBy"`
}

// amListAlertsParams holds the optional query parameters for GET /api/v2/alerts.
type amListAlertsParams struct {
	Active    *bool
	Silenced  *bool
	Inhibited *bool
	Filter    []string // matchers, e.g. `alertname="HighCPU"`
	Receiver  string
}

// ─── API Methods ─────────────────────────────────────────────────────────

// ListAlerts calls GET /api/v2/alerts with the given query parameters.
func (c *alertmanagerClient) ListAlerts(ctx context.Context, p *amListAlertsParams) ([]gettableAlert, error) {
	query := url.Values{}
	if p != nil {
		if p.Active != nil {
			query.Set("active", strconv.FormatBool(*p.Active))
		}
		if p.Silenced != nil {
			query.Set("silenced", strconv.FormatBool(*p.Silenced))
		}
		if p.Inhibited != nil {
			query.Set("inhibited", strconv.FormatBool(*p.Inhibited))
		}
		for _, f := range p.Filter {
			query.Add("filter", f)
		}
		if p.Receiver != "" {
			query.Set("receiver", p.Receiver)
		}
	}

	path := "/api/v2/alerts"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list Alertmanager alerts")
	}

	var alerts []gettableAlert
	if err := json.Unmarshal(body, &alerts); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Alertmanager alerts")
	}

	return alerts, nil
}

// PostAlerts calls POST /api/v2/alerts with the given alerts and expects a
// 200 {"status":"success"} response.
func (c *alertmanagerClient) PostAlerts(ctx context.Context, alerts []postableAlert) error {
	payload, err := json.Marshal(alerts)
	if err != nil {
		return errors.Wrap(err, "failed to marshal alerts")
	}

	if _, _, err := c.doRequest(ctx, http.MethodPost, "/api/v2/alerts", bytes.NewReader(payload)); err != nil {
		return errors.Wrap(err, "failed to post alerts to Alertmanager")
	}

	return nil
}
