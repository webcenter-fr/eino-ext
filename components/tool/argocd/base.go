package argocd

import (
	"emperror.dev/errors"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// baseTool holds shared state for all ArgoCD tools that require API clients.
type baseTool struct {
	clients        map[string]api.API
	knownInstances []string
}

// client returns the API client for the given instance name, or an error
// if the instance is not found among the known instances.
func (b *baseTool) client(instance string) (api.API, error) {
	c, ok := b.clients[instance]
	if !ok {
		return nil, instanceNotFoundError(instance, b.knownInstances)
	}
	return c, nil
}

// newBaseTool builds ArgoCD clients for all configured instances and returns
// a baseTool ready to be embedded by individual tools.
func newBaseTool(configs Configs) (*baseTool, error) {
	if len(configs) == 0 {
		return nil, errors.Errorf("at least one ArgoCD instance configuration is required")
	}
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}
	return &baseTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}, nil
}

// validateParams validates a struct using the shared validator instance.
func validateParams(v any) error {
	return validate.Struct(v)
}

// projectFilter returns a nil slice when project is empty, or a single-element
// slice containing the project name. This avoids sending []string{""} which
// some ArgoCD API versions interpret as a literal empty-string filter.
func projectFilter(project string) []string {
	if project == "" {
		return nil
	}
	return []string{project}
}
