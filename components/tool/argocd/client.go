package argocd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/sirupsen/logrus"
)

type Client struct {
	serverURL  string
	token      string
	httpClient *http.Client
}

func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("argocd config is nil")
	}
	if cfg.ServerURL == "" {
		return nil, errors.New("argocd serverURL is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("argocd token is required")
	}

	serverURL := strings.TrimRight(cfg.ServerURL, "/")

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.Insecure,
		},
	}

	return &Client{
		serverURL: serverURL,
		token:     cfg.Token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}, nil
}

func NewClients(configs Configs) (map[string]*Client, error) {
	clients := make(map[string]*Client)
	for name, cfg := range configs {
		client, err := NewClient(cfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", name)
		}
		clients[name] = client
	}
	return clients, nil
}

func (c *Client) ListApplications(ctx context.Context, selector, project, appNamespace string) (*ApplicationListResponse, error) {
	params := url.Values{}
	if selector != "" {
		params.Set("selector", selector)
	}
	if project != "" {
		params.Set("project", project)
	}
	if appNamespace != "" {
		params.Set("appNamespace", appNamespace)
	}

	path := "/api/v1/applications"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var result ApplicationListResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetApplication(ctx context.Context, name, appNamespace, project string) (*ApplicationResponse, error) {
	params := url.Values{}
	if appNamespace != "" {
		params.Set("appNamespace", appNamespace)
	}
	if project != "" {
		params.Set("project", project)
	}

	path := fmt.Sprintf("/api/v1/applications/%s", url.PathEscape(name))
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var result ApplicationResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SyncApplication(ctx context.Context, name, appNamespace, project string, req *SyncRequest) (*ApplicationResponse, error) {
	params := url.Values{}
	if appNamespace != "" {
		params.Set("appNamespace", appNamespace)
	}
	if project != "" {
		params.Set("project", project)
	}

	path := fmt.Sprintf("/api/v1/applications/%s/sync", url.PathEscape(name))
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var result ApplicationResponse
	if err := c.doRequest(ctx, http.MethodPost, path, req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CreateApplication(ctx context.Context, req *ApplicationCreateRequest, upsert bool) (*ApplicationResponse, error) {
	path := "/api/v1/applications"
	if upsert {
		path += "?upsert=true"
	}

	var result ApplicationResponse
	if err := c.doRequest(ctx, http.MethodPost, path, req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteApplication(ctx context.Context, name, appNamespace, project string, cascade *bool) error {
	params := url.Values{}
	if cascade != nil {
		if *cascade {
			params.Set("cascade", "true")
		} else {
			params.Set("cascade", "false")
		}
	}
	if appNamespace != "" {
		params.Set("appNamespace", appNamespace)
	}
	if project != "" {
		params.Set("project", project)
	}

	path := fmt.Sprintf("/api/v1/applications/%s", url.PathEscape(name))
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	if err := c.doRequest(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) ListProjects(ctx context.Context, name string) (*ProjectListResponse, error) {
	params := url.Values{}
	if name != "" {
		params.Set("name", name)
	}

	path := "/api/v1/projects"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var result ProjectListResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetProject(ctx context.Context, name string) (*ProjectResponse, error) {
	path := fmt.Sprintf("/api/v1/projects/%s", url.PathEscape(name))

	var result ProjectResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body, result interface{}) error {
	u := c.serverURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.Wrap(err, "failed to marshal request body")
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return errors.Wrap(err, "failed to create HTTP request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrapf(err, "failed to %s %s", method, path)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 10 * 1024 * 1024
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.Wrap(err, "failed to read response body")
	}
	if len(respBody) > maxResponseBytes {
		return errors.Errorf("argocd response exceeds %d bytes", maxResponseBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Message != "" {
			return errors.Errorf("argocd API error (HTTP %d): %s", resp.StatusCode, apiErr.Message)
		}
		if apiErr.Error != "" {
			return errors.Errorf("argocd API error (HTTP %d): %s", resp.StatusCode, apiErr.Error)
		}
		logrus.Debugf("argocd API raw error response (HTTP %d): %s", resp.StatusCode, string(respBody))
		return errors.Errorf("argocd API error (HTTP %d)", resp.StatusCode)
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return errors.Wrap(err, "failed to unmarshal response")
		}
	}

	return nil
}
