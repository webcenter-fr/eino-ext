package alertmanager

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Alertmanager tool names, shared across constructors, registry and check.
const (
	instanceListToolName = "alertmanager_instance_list"
	alertToolName        = "alertmanager_alert"
	alertWriteToolName   = "alertmanager_alert_write"
)

// baseTool holds shared client bundles for all Alertmanager tools.
type baseTool struct {
	clients        map[string]*alertmanagerClient
	knownInstances []string
}

func (b *baseTool) client(instance string) (*alertmanagerClient, error) {
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
		knownInstances: toolutil.SortedKeys(clients),
	}, nil
}
