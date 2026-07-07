package github

import (
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const defaultTimeout = 30 * time.Second

// NewClient creates a new GitHub API client using the provided configuration.
func NewClient(cfg *Config) (*github.Client, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: cfg.Timeout}
	gh := github.NewClient(httpClient).WithAuthToken(cfg.Token)

	if cfg.BaseURL != "" {
		uploadURL := cfg.UploadURL
		if uploadURL == "" {
			uploadURL = cfg.BaseURL
		}
		var err error
		gh, err = gh.WithEnterpriseURLs(cfg.BaseURL, uploadURL)
		if err != nil {
			return nil, errors.Wrap(err, "invalid GHES URLs")
		}
	}

	return gh, nil
}

// BuildClients creates GitHub clients for all configurations present in the
// Configs map. It returns a map of instance names to their corresponding
// clients, or an error if any client creation fails.
func BuildClients(configs Configs) (map[string]*github.Client, error) {
	clients := make(map[string]*github.Client)

	for instanceName, cfg := range configs {
		cfg := cfg
		client, err := NewClient(&cfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}

	return clients, nil
}
