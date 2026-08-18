package grafana

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// grafanaClient wraps an http.Client with Grafana instance details.
type grafanaClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	timeout    time.Duration
}

// NewClient creates a Grafana HTTP client from the provided configuration.
// It validates the config, applies defaults, and builds an *http.Client with
// TLS settings and timeout.
func NewClient(ctx context.Context, config Config) (*grafanaClient, error) {
	if config.DefaultTimeout == "" {
		config.DefaultTimeout = "30s"
	}

	if err := validate.Struct(&config); err != nil {
		return nil, errors.Wrap(err, "invalid Grafana config")
	}

	if !strings.HasPrefix(config.URL, "https://") && !strings.HasPrefix(config.URL, "http://") {
		return nil, errors.Errorf("Grafana URL must include scheme (https:// or http://): %s", config.URL)
	}

	parsedTimeout, err := parseTimeoutOrDefault(config.DefaultTimeout, 30*time.Second)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid default_timeout value: %s", config.DefaultTimeout)
	}

	var transport http.RoundTripper
	if config.TLSSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	} else {
		transport = http.DefaultTransport
	}

	hc := &http.Client{
		Transport: transport,
		Timeout:   parsedTimeout,
	}

	return &grafanaClient{
		baseURL:    strings.TrimRight(config.URL, "/"),
		token:      config.Token,
		httpClient: hc,
		timeout:    parsedTimeout,
	}, nil
}

// parseTimeoutOrDefault parses a Go duration string and returns it, or the
// default if the string is empty.
func parseTimeoutOrDefault(s string, defaultVal time.Duration) (time.Duration, error) {
	if s == "" {
		return defaultVal, nil
	}
	return time.ParseDuration(s)
}

// BuildClients creates grafanaClients for all configurations in the Configs map.
func BuildClients(ctx context.Context, configs Configs) (map[string]*grafanaClient, error) {
	clients := make(map[string]*grafanaClient)
	for instanceName, config := range configs {
		client, err := NewClient(ctx, config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}
	return clients, nil
}

// maxErrorBodyLen caps how much of a non-2xx response body is included in the
// returned error message. This prevents large or sensitive Grafana error bodies
// from being leaked verbatim through error chains.
const maxErrorBodyLen = 512

// maxResponseBodyLen caps how many bytes of a successful response body are read
// into memory. Prevents a broad range query from exhausting memory.
const maxResponseBodyLen = 10 << 20 // 10 MiB

// httpError is a typed error carrying the HTTP status code so callers can
// branch on it (e.g. 404 → not found) without parsing the error string.
type httpError struct {
	statusCode int
	method     string
	path       string
	body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("Grafana API error: HTTP %d %s %s: %s", e.statusCode, e.method, e.path, e.body)
}

// isHTTPStatus returns true if err is an *httpError with the given status.
func isHTTPStatus(err error, code int) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.statusCode == code
	}
	return false
}

// doRequest executes an HTTP request and returns the raw body.
// Returns an error for status >= 400. The error is an *httpError carrying the
// status code; its message includes a truncated response body to avoid leaking
// large or sensitive Grafana error payloads.
func (c *grafanaClient) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(reqCtx, method, fullURL, body)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "failed to create request %s %s", method, path)
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "request failed for %s %s", method, path)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyLen+1))
	if err != nil {
		return nil, resp.StatusCode, errors.Wrap(err, "failed to read response body")
	}
	if len(respBody) > maxResponseBodyLen {
		return nil, resp.StatusCode, errors.Errorf("response body exceeds %d bytes", maxResponseBodyLen)
	}

	if resp.StatusCode >= 400 {
		snippet := string(respBody)
		if len(snippet) > maxErrorBodyLen {
			snippet = snippet[:maxErrorBodyLen] + "...(truncated)"
		}
		return nil, resp.StatusCode, &httpError{
			statusCode: resp.StatusCode,
			method:     method,
			path:       path,
			body:       snippet,
		}
	}

	return respBody, resp.StatusCode, nil
}

// ─── Wire types ──────────────────────────────────────────────────────────

// searchParams maps to GET /api/search query parameters.
type searchParams struct {
	Query      string
	Type       string
	Tags       []string
	FolderUIDs []string
	Sort       string
	Limit      int
	Page       int
}

// searchHit is a single element of the GET /api/search response array.
type searchHit struct {
	ID          int64    `json:"id"`
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URL         string   `json:"url"` // relative: /d/<uid>/<slug>
	Type        string   `json:"type"`
	Tags        []string `json:"tags"`
	FolderTitle string   `json:"folderTitle"`
	FolderUID   string   `json:"folderUid"`
	Starred     bool     `json:"starred"`
}

// dashboardResponse is the GET /api/dashboards/uid/:uid response.
type dashboardResponse struct {
	Dashboard map[string]any `json:"dashboard"`
	Meta      dashboardMeta  `json:"meta"`
}

type dashboardMeta struct {
	FolderTitle string `json:"folderTitle"`
	FolderUID   string `json:"folderUid"`
	FolderID    int64  `json:"folderId"`
	Version     int    `json:"version"`
	CreatedBy   string `json:"createdBy"`
	UpdatedBy   string `json:"updatedBy"`
}

// saveDashboardRequest is the POST /api/dashboards/db request body.
type saveDashboardRequest struct {
	Dashboard map[string]any `json:"dashboard"`
	FolderUID string         `json:"folderUid,omitempty"`
	FolderID  int64          `json:"folderId,omitempty"`
	Message   string         `json:"message,omitempty"`
	Overwrite bool           `json:"overwrite"`
}

// saveDashboardResponse is the POST /api/dashboards/db response.
type saveDashboardResponse struct {
	ID      int64  `json:"id"`
	UID     string `json:"uid"`
	URL     string `json:"url"`    // relative: /d/<uid>/<slug>
	Status  string `json:"status"` // "success" or "version-mismatch"
	Version int    `json:"version"`
	Slug    string `json:"slug"`
}

// deleteDashboardResponse is the DELETE /api/dashboards/uid/:uid response.
type deleteDashboardResponse struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	ID      int64  `json:"id"`
}

// dataSource is a single element of GET /api/datasources and the body of
// GET /api/datasources/uid/:uid. Sensitive top-level fields (password,
// basicAuthPassword, secureJsonData) are intentionally NOT declared here so
// they are dropped during unmarshal and never enter our memory as named fields.
type dataSource struct {
	ID               int64           `json:"id"`
	UID              string          `json:"uid"`
	OrgID            int64           `json:"orgId"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	TypeName         string          `json:"typeName,omitempty"`
	TypeLogoURL      string          `json:"typeLogoUrl,omitempty"`
	Access           string          `json:"access"`
	URL              string          `json:"url"`
	User             string          `json:"user"`
	Database         string          `json:"database"`
	BasicAuth        bool            `json:"basicAuth"`
	BasicAuthUser    string          `json:"basicAuthUser,omitempty"`
	WithCredentials  bool            `json:"withCredentials,omitempty"`
	IsDefault        bool            `json:"isDefault"`
	JSONData         map[string]any  `json:"jsonData,omitempty"`
	SecureJSONFields map[string]bool `json:"secureJsonFields,omitempty"` // kept on wire, excluded from output
	ReadOnly         bool            `json:"readOnly"`
	Version          int             `json:"version"`
}

// ─── dataSource output mapping ──────────────────────────────────────────

// toDescribeOutput maps a dataSource to its full (describe) output, redacting
// sensitive jsonData values. It is the single source of truth for the field
// mapping; toListOutput projects the lean list view from it.
func (ds dataSource) toDescribeOutput() DataSourceDescribeOutput {
	return DataSourceDescribeOutput{
		ID:              ds.ID,
		UID:             ds.UID,
		OrgID:           ds.OrgID,
		Name:            ds.Name,
		Type:            ds.Type,
		TypeName:        ds.TypeName,
		TypeLogoURL:     ds.TypeLogoURL,
		Access:          ds.Access,
		URL:             ds.URL,
		User:            ds.User,
		Database:        ds.Database,
		BasicAuth:       ds.BasicAuth,
		BasicAuthUser:   ds.BasicAuthUser,
		WithCredentials: ds.WithCredentials,
		IsDefault:       ds.IsDefault,
		JSONData:        redactedJSONData(ds.JSONData),
		ReadOnly:        ds.ReadOnly,
		Version:         ds.Version,
	}
}

// toListOutput maps a dataSource to its lean (list) output: the describe
// output minus the extended fields (orgId, typeLogoUrl, user, database,
// basicAuth, basicAuthUser, withCredentials). jsonData is already redacted.
func (ds dataSource) toListOutput() DataSourceListOutput {
	full := ds.toDescribeOutput()
	return DataSourceListOutput{
		ID:        full.ID,
		UID:       full.UID,
		Name:      full.Name,
		Type:      full.Type,
		TypeName:  full.TypeName,
		URL:       full.URL,
		Access:    full.Access,
		IsDefault: full.IsDefault,
		ReadOnly:  full.ReadOnly,
		Version:   full.Version,
		JSONData:  full.JSONData,
	}
}

// ─── API Methods ─────────────────────────────────────────────────────────

// SearchDashboards calls GET /api/search with the given query parameters.
// Returns the raw JSON array body.
func (c *grafanaClient) SearchDashboards(ctx context.Context, params *searchParams) ([]byte, error) {
	query := url.Values{}
	if params.Query != "" {
		query.Set("query", params.Query)
	}
	if params.Type != "" {
		query.Set("type", params.Type)
	}
	for _, tag := range params.Tags {
		query.Add("tag", tag)
	}
	if len(params.FolderUIDs) > 0 {
		query.Set("folderUIDs", strings.Join(params.FolderUIDs, ","))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Page > 0 {
		query.Set("page", strconv.Itoa(params.Page))
	}

	path := "/api/search"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to search dashboards")
	}
	return body, nil
}

// GetDashboard calls GET /api/dashboards/uid/:uid and returns the raw body.
// The uid is path-escaped to prevent path traversal / endpoint injection
// (e.g. a uid containing ".." or "/" must not alter the request path).
func (c *grafanaClient) GetDashboard(ctx context.Context, uid string) ([]byte, error) {
	path := "/api/dashboards/uid/" + url.PathEscape(uid)
	body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get dashboard")
	}
	return body, nil
}

// SaveDashboard calls POST /api/dashboards/db with the given payload.
// Returns the raw response body (contains id, uid, url, version, status).
func (c *grafanaClient) SaveDashboard(ctx context.Context, payload []byte) ([]byte, error) {
	body, _, err := c.doRequest(ctx, http.MethodPost, "/api/dashboards/db", strings.NewReader(string(payload)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to save dashboard")
	}
	return body, nil
}

// DeleteDashboard calls DELETE /api/dashboards/uid/:uid.
func (c *grafanaClient) DeleteDashboard(ctx context.Context, uid string) ([]byte, error) {
	path := "/api/dashboards/uid/" + url.PathEscape(uid)
	body, _, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to delete dashboard")
	}
	return body, nil
}

// ListDataSources calls GET /api/datasources and returns the raw JSON array.
// The endpoint does not support pagination; Grafana caps results at 5000.
func (c *grafanaClient) ListDataSources(ctx context.Context) ([]byte, error) {
	body, _, err := c.doRequest(ctx, http.MethodGet, "/api/datasources", nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list data sources")
	}
	return body, nil
}

// GetDataSource calls GET /api/datasources/uid/:uid and returns the raw body.
// The uid is path-escaped to prevent path traversal / endpoint injection
// (e.g. a uid containing ".." or "/" must not alter the request path), mirroring
// GetDashboard.
func (c *grafanaClient) GetDataSource(ctx context.Context, uid string) ([]byte, error) {
	path := "/api/datasources/uid/" + url.PathEscape(uid)
	body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get data source")
	}
	return body, nil
}
