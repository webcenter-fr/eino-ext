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

const clusterDescribeDescription = `
** General Purpose **
It gets the details of a specific ArgoCD cluster.

** Output **
It returns a JSON object representing the ArgoCD cluster. The credentials are not included in the output.
`

type ClusterDescribeParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name                string   `json:"name" validate:"required" jsonschema:"(required) The cluster name."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata info" jsonschema:"(optional) Fields to exclude: 'metadata', 'info'."`
}

type ClusterDescribeOutput struct {
	Metadata *api.ObjectMeta  `json:"metadata,omitempty"`
	Info     *api.ClusterInfo `json:"info,omitempty"`
}

type ClusterDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ClusterDescribeTool) Invoke(ctx context.Context, params *ClusterDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	cluster, err := c.Cluster().Get("", &api.ClusterQueryOptions{Name: params.Name})
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster")
	}
	// Direct Name field shadows embedded ObjectMeta.Name, copy it over
	cluster.ObjectMeta.Name = cluster.Name

	output := &ClusterDescribeOutput{
		Metadata: &cluster.ObjectMeta,
		Info:     &cluster.Info,
	}

	for _, excludeField := range params.ExcludeFieldsOutput {
		switch excludeField {
		case "metadata":
			output.Metadata = nil
		case "info":
			output.Info = nil
		default:
			return "", errors.Errorf("invalid exclude field: %s", excludeField)
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewClusterDescribeTool(ctx context.Context, configs Configs) (*ClusterDescribeTool, error) {
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	describeTool := &ClusterDescribeTool{baseTool: base}
	t, err := utils.InferTool("argocd_cluster_describe", fmt.Sprintf("%s\n%s", clusterDescribeDescription, describeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
