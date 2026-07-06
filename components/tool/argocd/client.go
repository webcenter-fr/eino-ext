package argocd

import (
	"strings"

	"emperror.dev/errors"
	"github.com/disaster37/goargocdclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/disaster37/goargocdclient/api"
)

// NewClient creates a new ArgoCD client using the provided configuration. It returns the client and any error encountered during the creation process.
func NewClient(config Config) (c api.API, err error) {
	if err := validate.Struct(&config); err != nil {
		return nil, errors.Wrap(err, "invalid ArgoCD config")
	}

	if !strings.HasPrefix(config.Url, "https://") && !strings.HasPrefix(config.Url, "http://") {
		return nil, errors.Errorf("ArgoCD URL must include scheme (https:// or http://): %s", config.Url)
	}

	// client
	if config.Options != nil {
		c, err = goargocdclient.New(config.Url, config.Options...)
	} else {
		c, err = goargocdclient.New(config.Url)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to create ArgoCD client")
	}

	return c, nil
}

// BuildClients creates ArgoCD clients for all configurations present in the Configs map. It returns a map of instance names to their corresponding ArgoCD clients, or an error if any client creation fails.
func BuildClients(configs Configs) (clients map[string]api.API, err error) {
	clients = make(map[string]api.API)

	for instanceName, config := range configs {
		client, err := NewClient(config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for instance %s", instanceName)
		}
		clients[instanceName] = client
	}

	return clients, nil
}
