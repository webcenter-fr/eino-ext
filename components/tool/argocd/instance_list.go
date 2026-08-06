package argocd

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

const instanceListDescription = `
** General Purpose **
It lists all the ArgoCD instances where it can connect.

** Output **
It returns a JSON array of objects, where each object represents an instance with the following fields:
- name: the name of the ArgoCD instance.
`

// InstanceListTool is an eino tool for listing configured ArgoCD instances.
type InstanceListTool struct {
	knownInstances []string
	tool.InvokableTool
}

// InstanceListParams defines the parameters for listing ArgoCD instances.
type InstanceListParams struct{}

// Invoke returns known ArgoCD instances as JSON.
func (t *InstanceListTool) Invoke(ctx context.Context, params *InstanceListParams) (string, error) {
	b, err := json.Marshal(t.knownInstances)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal known instances")
	}
	return string(b), nil
}

// NewInstanceListTool creates a new InstanceListTool.
func NewInstanceListTool(ctx context.Context, configs Configs) (*InstanceListTool, error) {
	instanceListTool := &InstanceListTool{
		knownInstances: configs.GetInstanceNames(),
	}

	invokable, err := utils.InferTool("argocd_instance_list", instanceListDescription, instanceListTool.Invoke,
		utils.WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()))
	if err != nil {
		return nil, err
	}
	instanceListTool.InvokableTool = invokable

	return instanceListTool, nil
}
