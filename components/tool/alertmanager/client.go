package alertmanager

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/go-openapi/runtime"
	httpclient "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// alertmanagerClient wraps the official Alertmanager v2 client
// (github.com/prometheus/alertmanager/api/v2/client.AlertmanagerAPI) and
// carries the per-request timeout.
type alertmanagerClient struct {
	api     *client.AlertmanagerAPI
	timeout time.Duration
}

// amMaxErrorBodyLen caps how much of a non-2xx response body is included in
// the redacted error message surfaced to callers. Preserved from the
// hand-rolled client so a server echoing the Authorization header cannot leak
// large or sensitive payloads (CWE-532 / CWE-200).
const amMaxErrorBodyLen = 512

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
// truncated and surfaced in error messages, so credentials sent in the
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

// NewClient creates an Alertmanager client from config, wrapping the official
// Alertmanager v2 client with a redacting transport.
func NewClient(ctx context.Context, config Config) (*alertmanagerClient, error) {
	if config.Timeout == "" {
		config.Timeout = "30s"
	}

	if err := validate.Struct(&config); err != nil {
		return nil, errors.Wrap(err, "invalid Alertmanager config")
	}

	if !strings.HasPrefix(config.Address, "http://") && !strings.HasPrefix(config.Address, "https://") {
		return nil, errors.Errorf("Alertmanager address must include scheme (http:// or https://): %s", config.Address)
	}

	timeout, err := time.ParseDuration(config.Timeout)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid Alertmanager timeout value: %s", config.Timeout)
	}

	u, err := url.Parse(config.Address)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid Alertmanager address: %s", config.Address)
	}
	// u.Scheme is already guaranteed http/https by the scheme check above.
	host := u.Host // host:port, no scheme, no path
	// Map any user-supplied path prefix onto the API base path. The official
	// client's DefaultBasePath is "/api/v2/"; we preserve a user prefix such
	// as "http://am:9093/prefix" -> basePath "/prefix/api/v2/".
	prefix := strings.Trim(u.Path, "/")
	basePath := "/api/v2/"
	if prefix != "" {
		basePath = "/" + prefix + "/api/v2/"
	}
	schemes := []string{u.Scheme}

	httpClient := &http.Client{
		Timeout: timeout,
		// Defense-in-depth against credential leakage via server-initiated
		// redirects (CWE-200): the Alertmanager v2 API does not redirect, so
		// any redirect is either a misconfigured proxy or a compromised
		// server. Strip the Authorization header on every redirect so a
		// redirect target (even on the same host) cannot receive the
		// configured Bearer/Basic credentials. The Go default only strips
		// sensitive headers on cross-HOSTNAME redirects, leaving same-host
		// different-port redirects exposed.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Authorization")
			return nil
		},
	}
	if config.TLSSkipVerify {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}

	rt := httpclient.NewWithClient(host, basePath, schemes, httpClient)

	var auth runtime.ClientAuthInfoWriter
	switch {
	case config.BearerToken != "":
		auth = httpclient.BearerToken(config.BearerToken)
	case config.Username != "" || config.Password != "":
		auth = httpclient.BasicAuth(config.Username, config.Password)
	}
	if auth != nil {
		rt.DefaultAuthentication = auth
	}

	secrets := buildRedactSecrets(config.Username, config.Password, config.BearerToken)
	redacting := &redactingTransport{base: rt, secrets: secrets}

	api := client.New(redacting, strfmt.Default)

	return &alertmanagerClient{api: api, timeout: timeout}, nil
}

// BuildClients builds an Alertmanager client for every instance in configs.
func BuildClients(ctx context.Context, configs Configs) (map[string]*alertmanagerClient, error) {
	clients := make(map[string]*alertmanagerClient, len(configs))
	for instanceName, cfg := range configs {
		c, err := NewClient(ctx, cfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create Alertmanager client for instance %s", instanceName)
		}
		clients[instanceName] = c
	}
	return clients, nil
}

// redactingTransport wraps a runtime.ClientTransport and redacts configured
// secrets from the Payload of non-2xx error responses returned by the
// official Alertmanager client, then truncates the payload to
// amMaxErrorBodyLen. This preserves the hand-rolled client's defense against
// a server (or proxy) that echoes the Authorization header in error bodies
// (CWE-532 / CWE-200): credentials sent in the Authorization header cannot
// leak back through error messages surfaced to the LLM.
//
// Redaction is applied ONLY to error payloads (the generated 400/500
// response structs); success bodies are passed through untouched.
type redactingTransport struct {
	base    runtime.ClientTransport
	secrets []string
}

func (t *redactingTransport) Submit(op *runtime.ClientOperation) (any, error) {
	result, err := t.base.Submit(op)
	if err != nil {
		redactAPIErrorPayload(err, t.secrets)
	}
	return result, err
}

// redactAPIErrorPayload redacts and truncates the Payload of the generated
// Alertmanager GET 400/500 response structs in place. Mutating Payload makes the
// surfaced err.Error() redacted while preserving the typed error so callers
// can still errors.As / type-assert.
//
// It also type-switches on *runtime.APIError, the fallback error returned by
// the go-openapi runtime for status codes outside the typed 400/500 set (e.g.
// 401, 403, 404, 429, 502, 503). For go-openapi v0.33.0 the APIError.Error()
// method json-marshals the response struct, whose only field is unexported,
// producing "{}" — so no body is leaked today. This case is defense-in-depth
// against a future go-openapi change that surfaces the body: if the rendered
// error string is found to contain a configured secret, the Response field is
// replaced with a redacted, truncated placeholder so the secret cannot reach
// the LLM (CWE-532 / CWE-200).
func redactAPIErrorPayload(err error, secrets []string) {
	switch e := err.(type) {
	case *alert.GetAlertsBadRequest:
		e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
	case *alert.GetAlertsInternalServerError:
		e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
	case *runtime.APIError:
		// Defense-in-depth: see comment above. For v0.33.0 this branch is a
		// no-op because e.Error() renders "{}" and contains no secrets.
		rendered := e.Error()
		redacted := truncateRedacted(redactSecret(rendered, secrets))
		if redacted != rendered {
			e.Response = redacted
		}
	}
}

func truncateRedacted(s string) string {
	if len(s) > amMaxErrorBodyLen {
		return s[:amMaxErrorBodyLen] + "...(truncated)"
	}
	return s
}

// listAlertsParams holds the optional query parameters for GET /api/v2/alerts
// that the alert tools use. It maps onto alert.GetAlertsParams.
type listAlertsParams struct {
	Active    *bool
	Silenced  *bool
	Inhibited *bool
	Filter    []string // matchers, e.g. `alertname="HighCPU"`; one matcher per element
	Receiver  string   // empty means unset
}

// boolPtr returns a pointer to the given bool. Used to distinguish "unset"
// (nil) from an explicit true/false in GetAlertsParams fields.
func boolPtr(b bool) *bool { return &b }

// ListAlerts calls GET /api/v2/alerts with the given query parameters.
func (c *alertmanagerClient) ListAlerts(ctx context.Context, p *listAlertsParams) ([]*models.GettableAlert, error) {
	params := alert.NewGetAlertsParamsWithContext(ctx)
	if p != nil {
		if p.Active != nil {
			params.SetActive(p.Active)
		}
		if p.Silenced != nil {
			params.SetSilenced(p.Silenced)
		}
		if p.Inhibited != nil {
			params.SetInhibited(p.Inhibited)
		}
		if len(p.Filter) > 0 {
			params.SetFilter(p.Filter)
		}
		if p.Receiver != "" {
			r := p.Receiver
			params.SetReceiver(&r)
		}
	}
	resp, err := c.api.Alert.GetAlerts(params)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list Alertmanager alerts")
	}
	return resp.GetPayload(), nil
}
