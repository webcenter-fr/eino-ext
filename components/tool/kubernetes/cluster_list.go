package kubernetes

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const clusterListDescription = `
** General Purpose **
It lists all the kubernetes clusters where it can use.

** Output **
It return a JSON array of objects, where each object represents a cluster with the following fields:
- name: the name of the kubernetes cluster.
`

// ClusterListTool is a tool that lists all the kubernetes clusters where it can use. It implements the InvokableTool interface.
type ClusterListTool struct {
	knownClusters []string

	tool.InvokableTool
}

// Invoke executes the DescribeTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *ClusterListTool) Invoke(ctx context.Context, params map[string]any) (string, error) {

	b, err := json.Marshal(t.knownClusters)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal known clusters")
	}
	return string(b), nil
}

// NewClusterListTool creates a new instance of the ClusterListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewClusterListTool(ctx context.Context, configs Configs) (*ClusterListTool, error) {
	clusterListTool := &ClusterListTool{
		knownClusters: configs.GetClusterNames(),
	}

	// Wire the non-streaming (invokable) path.
	invokable, err := utils.InferTool("kubernetes_cluster_list", clusterListDescription, clusterListTool.Invoke)
	if err != nil {
		return nil, err
	}
	clusterListTool.InvokableTool = invokable

	return clusterListTool, nil
}
