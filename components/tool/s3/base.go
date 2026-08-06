package s3

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// baseTool holds shared client bundles for all S3 tools.
type baseTool struct {
	clients        map[string]Client
	knownInstances []string
	configs        Configs
}

func (b *baseTool) client(instance string) (Client, error) {
	c, ok := b.clients[instance]
	if !ok {
		return nil, toolutil.NotFoundError("S3 bucket instance", instance, b.knownInstances)
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
		configs:        configs,
	}, nil
}

func newBaseToolWithClients(configs Configs, clients map[string]Client) *baseTool {
	return &baseTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
		configs:        configs,
	}
}
