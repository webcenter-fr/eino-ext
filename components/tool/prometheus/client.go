package prometheus

import (
	"context"
	"crypto/tls"
	"net/http"

	"emperror.dev/errors"

	"github.com/prometheus/client_golang/api"
	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// authRoundTripper wraps an http.RoundTripper and injects authentication headers.
// Only one auth method should be configured: Bearer token takes priority over Basic auth.
type authRoundTripper struct {
	rt          http.RoundTripper
	username    string
	password    string
	bearerToken string
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Set Basic auth first, then Bearer so Bearer takes priority (it overwrites
	// the same Authorization header set by SetBasicAuth).
	if a.username != "" {
		req.SetBasicAuth(a.username, a.password)
	}
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}
	return a.rt.RoundTrip(req)
}

// NewClient creates a new Prometheus API client from the given configuration.
func NewClient(ctx context.Context, config Config) (promapi.API, error) {
	if err := validate.Struct(&config); err != nil {
		return nil, errors.Wrap(err, "invalid Prometheus config")
	}

	rt := api.DefaultRoundTripper

	if config.TLSSkipVerify {
		t, ok := rt.(*http.Transport)
		if !ok {
			return nil, errors.New("cannot set TLSSkipVerify: DefaultRoundTripper is not *http.Transport")
		}
		clone := t.Clone()
		if clone.TLSClientConfig == nil {
			clone.TLSClientConfig = &tls.Config{}
		}
		clone.TLSClientConfig.InsecureSkipVerify = true
		rt = clone
	}

	rt = &authRoundTripper{
		rt:          rt,
		username:    config.Username,
		password:    config.Password,
		bearerToken: config.BearerToken,
	}

	client, err := api.NewClient(api.Config{
		Address:      config.Address,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Prometheus API client")
	}

	return promapi.NewAPI(client), nil
}

// BuildClients creates Prometheus API clients for all configurations in the Configs map.
func BuildClients(ctx context.Context, configs Configs) (clients map[string]promapi.API, err error) {
	clients = make(map[string]promapi.API)
	for instanceName, config := range configs {
		client, err := NewClient(ctx, config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}
	return clients, nil
}
