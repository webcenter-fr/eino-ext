package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/goccy/go-json"
)

const clusterListDescription = `
** General Purpose **
It lists all ArgoCD clusters accessible to the configured instance.

** Output **
It returns a JSON array of objects, where each object represents a cluster with the following fields:
- name: the name of the cluster.
- server: the server address of the cluster.
- project: the ArgoCD project the cluster belongs to.
`

type ClusterListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each cluster JSON."`
}

type ClusterListOutput struct {
	Name    string `json:"name"`
	Server  string `json:"server"`
	Project string `json:"project"`
}

type ClusterListTool struct {
	clients        map[string]api.API
	knownInstances []string

	tool.InvokableTool
}

func (t *ClusterListTool) Invoke(ctx context.Context, params *ClusterListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	filter, err := CompileFilter(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	resp, err := c.Cluster().List(&api.ClusterQueryOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list clusters")
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := ClusterListOutput{
			Name:    item.Name,
			Server:  item.Server,
			Project: item.Project,
		}

		outputJSON := json.RawMessage(MustMarshal(output))
		if !IsMatch(outputJSON, filter) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewClusterListTool(ctx context.Context, configs Configs) (*ClusterListTool, error) {
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}

	listTool := &ClusterListTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_cluster_list", fmt.Sprintf("%s\n%s", clusterListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
