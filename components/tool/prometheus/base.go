package prometheus

import (
	"context"

	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
)

// baseTool holds shared client bundles for all Prometheus tools.
type baseTool struct {
	clients        map[string]promapi.API
	knownInstances []string
}

func (b *baseTool) client(instance string) (promapi.API, error) {
	c, ok := b.clients[instance]
	if !ok {
		return nil, instanceNotFoundError(instance, b.knownInstances)
	}
	return c, nil
}

func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
	clients, err := BuildClients(ctx, configs)
	if err != nil {
		return nil, err
	}
	return &baseTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}, nil
}
