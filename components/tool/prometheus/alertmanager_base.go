package prometheus

import (
	"context"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Alertmanager tool names, shared across constructors, registry and check.
const (
	// alertmanagerAlertListToolName removed — folded into prometheus_alert.
	alertWriteToolName = "prometheus_alert_write" // renamed from alertmanagerAlertWriteToolName
)

// alertmanagerBaseTool holds shared Alertmanager client bundles for the
// Alertmanager tools. It is separate from baseTool (Prometheus clients) because
// an instance only gets an Alertmanager client when its Config.Alertmanager
// field is non-nil.
type alertmanagerBaseTool struct {
	amClients      map[string]*alertmanagerClient
	knownInstances []string
}

func (b *alertmanagerBaseTool) amClient(instance string) (*alertmanagerClient, error) {
	c, ok := b.amClients[instance]
	if !ok {
		return nil, instanceNotFoundError(instance, b.knownInstances)
	}
	return c, nil
}

func newAlertmanagerBaseTool(ctx context.Context, configs Configs) (*alertmanagerBaseTool, error) {
	clients, err := BuildAlertmanagerClients(ctx, configs)
	if err != nil {
		return nil, err
	}
	return &alertmanagerBaseTool{
		amClients:      clients,
		knownInstances: toolutil.SortedKeys(clients),
	}, nil
}
