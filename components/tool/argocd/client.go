package argocd

import (
	"context"
	"strings"

	"emperror.dev/errors"
	"github.com/disaster37/goargocdclient"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// NewClient creates a new ArgoCD client using the provided configuration.
// It auto-derives goargocdclient options from Token and TLSSkipVerify
// convenience fields, prepending them before any explicit Options.
func NewClient(ctx context.Context, config Config) (c api.API, err error) {
	if err := validate.Struct(&config); err != nil {
		return nil, errors.Wrap(err, "invalid ArgoCD config")
	}

	if !strings.HasPrefix(config.URL, "https://") && !strings.HasPrefix(config.URL, "http://") {
		return nil, errors.Errorf("ArgoCD URL must include scheme (https:// or http://): %s", config.URL)
	}

	// Build options: convenience fields first, then explicit Options override.
	opts := make([]goargocdclient.Option, 0, len(config.Options)+2)
	if config.TLSSkipVerify {
		opts = append(opts, goargocdclient.WithInsecure())
	}
	if config.Token != "" {
		opts = append(opts, goargocdclient.WithToken(config.Token))
	}
	opts = append(opts, config.Options...)

	c, err = goargocdclient.New(config.URL, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create ArgoCD client")
	}

	return c, nil
}

// BuildClients creates ArgoCD clients for all configurations present in the Configs map. It returns a map of instance names to their corresponding ArgoCD clients, or an error if any client creation fails.
func BuildClients(ctx context.Context, configs Configs) (clients map[string]api.API, err error) {
	clients = make(map[string]api.API)

	for instanceName, config := range configs {
		client, err := NewClient(ctx, config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}

	return clients, nil
}
