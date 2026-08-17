package alertmanager

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
It lists all the Alertmanager instances where it can connect.

** Output **
It returns a JSON array of objects, where each object represents an instance with the following fields:
- name: the name of the Alertmanager instance.
`

// InstanceListTool lists all configured Alertmanager instances. It implements tool.InvokableTool.
type InstanceListTool struct {
	knownInstances []string
	tool.InvokableTool
}

// InstanceListParams holds the parameters for InstanceListTool (none required).
type InstanceListParams struct{}

// Invoke returns the configured instance names as a JSON string array.
func (t *InstanceListTool) Invoke(ctx context.Context, params *InstanceListParams) (string, error) {
	b, err := json.Marshal(t.knownInstances)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal known instances")
	}
	return string(b), nil
}

// NewInstanceListTool creates a new InstanceListTool for the given configs.
func NewInstanceListTool(ctx context.Context, configs Configs) (*InstanceListTool, error) {
	instanceListTool := &InstanceListTool{
		knownInstances: configs.GetInstanceNames(),
	}

	invokable, err := utils.InferTool(instanceListToolName, instanceListDescription, instanceListTool.Invoke,
		utils.WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()))
	if err != nil {
		return nil, err
	}
	instanceListTool.InvokableTool = invokable

	return instanceListTool, nil
}
