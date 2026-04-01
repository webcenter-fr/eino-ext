package kubernetes

import (
	"bytes"
	"context"
	"io"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-playground/validator/v10"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

const podLogDescription = `
** General Purpose **
It gets the logs of a specific pod in a specified Kubernetes cluster.

The log lines can be filtered using a regex pattern, and the number of log lines can be limited using the maxLines parameter.

** Output **
It return a Raw string representing the logs of the pod. Each log line is separated by a newline character.

`

// PodLogParams defines the parameters for the PodLog function, which gets the logs of a specific pod in a specified Kubernetes cluster. It includes the cluster name, namespace, and pod name.
type PodLogParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pod."`
	Name          string `json:"name" validate:"required" jsonschema:"(required) The pod name."`
	Container     string `json:"container,omitempty" validate:"omitempty" jsonschema:"(optional) The container name. If not specified, logs from the first container will be returned."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500,default=100" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
	FilterPattern string `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A regex pattern to filter log lines. Only log lines matching the pattern will be returned."`
}

// PodLogTool is a tool that gets the logs of a specific pod in a specified Kubernetes cluster. It contains a map of Kubernetes clients for different clusters and implements the InvokableTool interface.
type PodLogTool struct {
	clients map[string]*kubernetes.Clientset
	tool.InvokableTool
	knownClusters []string
}

// Invoke executes the PodLogTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *PodLogTool) Invoke(ctx context.Context, params *PodLogParams) (result string, err error) {

	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return "", errors.Wrap(err, "invalid parameters for PodLogTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return "", errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	req := c.CoreV1().Pods(params.Namespace).GetLogs(params.Name, &corev1.PodLogOptions{
		Container: params.Container,
		Follow:    false,
		TailLines: &params.MaxLines,
	})
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to get pod logs")
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", errors.Wrap(err, "Error when copy buffer")
	}

	return buf.String(), nil
}

// Invoke in stream mode
func (t *PodLogTool) InvokeStream(ctx context.Context, params *PodLogParams) (io.ReadCloser, error) {

	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return nil, errors.Wrap(err, "invalid parameters for PodLogTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return nil, errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	req := c.CoreV1().Pods(params.Namespace).GetLogs(params.Name, &corev1.PodLogOptions{
		Container: params.Container,
		Follow:    false,
		TailLines: &params.MaxLines,
	})
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get pod logs")
	}

	return podLogs, nil
}

// NewPodLogTool creates a new instance of the PodLogTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewPodLogTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {

	podLogTool := &PodLogTool{
		knownClusters: configs.GetClusterNames(),
	}
	clients, err := BuildClientSets(configs, nil)
	if err != nil {
		return nil, err
	}
	podLogTool.clients = clients

	// Infer tool
	t, err := utils.InferTool("kubernetes_pod_logs", podLogDescription, podLogTool.Invoke)
	if err != nil {
		return nil, err
	}
	podLogTool.InvokableTool = t

	return podLogTool, nil
}
