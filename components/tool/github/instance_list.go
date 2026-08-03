package github

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const instanceListDescription = `
** General Purpose **
It lists all the GitHub instances where it can connect.

** Output **
It returns a JSON array of strings, where each string is the name of a configured GitHub instance.
Use these instance names when calling any other GitHub tool.
`

type InstanceListTool struct {
	knownInstances []string
	tool.InvokableTool
}

type InstanceListParams struct{}

func (t *InstanceListTool) Invoke(ctx context.Context, params *InstanceListParams) (string, error) {
	b, err := json.Marshal(t.knownInstances)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal known instances")
	}
	return string(b), nil
}

func newInstanceListTool(ctx context.Context, knownInstances []string) (*InstanceListTool, error) {
	instanceListTool := &InstanceListTool{
		knownInstances: knownInstances,
	}

	invokable, err := utils.InferTool("github_instance_list", instanceListDescription, instanceListTool.Invoke)
	if err != nil {
		return nil, err
	}
	instanceListTool.InvokableTool = invokable

	return instanceListTool, nil
}
