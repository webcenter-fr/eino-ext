package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
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
	*baseTool
	tool.InvokableTool
}

func (t *ClusterListTool) Invoke(ctx context.Context, params *ClusterListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	resp, err := c.Cluster().List(&api.ClusterQueryOptions{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list clusters")
	}

	return filterMapMarshal(resp.Items, re, func(item *api.ClusterModel) ClusterListOutput {
		return ClusterListOutput{
			Name:    item.Name,
			Server:  item.Server,
			Project: item.Project,
		}
	})
}

func NewClusterListTool(ctx context.Context, configs Configs) (*ClusterListTool, error) {
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	listTool := &ClusterListTool{baseTool: base}
	t, err := utils.InferTool("argocd_cluster_list", fmt.Sprintf("%s\n%s", clusterListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
