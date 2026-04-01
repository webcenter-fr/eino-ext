package kubernetes

import (
	"bufio"
	"context"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
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
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
	FilterPattern string `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A regex pattern to filter log lines. Only log lines matching the pattern will be returned."`
}

// PodLogTool is a tool that gets the logs of a specific pod in a specified Kubernetes cluster.
// It implements both tool.InvokableTool (blocking, returns full log as string) and
// tool.StreamableTool (streaming, returns log lines as a schema.StreamReader).
type PodLogTool struct {
	clients       map[string]*kubernetes.Clientset
	knownClusters []string

	tool.InvokableTool
	tool.StreamableTool
}

// validate validates the given PodLogParams.
func (t *PodLogTool) validate(params *PodLogParams) (*kubernetes.Clientset, error) {
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	v := validator.New()
	if err := v.Struct(params); err != nil {
		return nil, errors.Wrap(err, "invalid parameters for PodLogTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return nil, errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	return c, nil
}

// Invoke gets the logs of a pod and returns them as a single string (non-streaming).
func (t *PodLogTool) Invoke(ctx context.Context, params *PodLogParams) (string, error) {
	c, err := t.validate(params)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(params.FilterPattern)

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

	buf := bufio.NewScanner(podLogs)
	var logs []string
	for buf.Scan() {
		if re.MatchString(buf.Text()) {
			logs = append(logs, buf.Text())
		}
	}
	if err := buf.Err(); err != nil {
		return "", errors.Wrap(err, "error reading pod logs")
	}

	return strings.Join(logs, "\n"), nil
}

// InvokeAsStream gets the logs of a pod and returns them line-by-line as a schema.StreamReader[string].
// Each string chunk in the stream is one log line (without the trailing newline).
// The caller must close the returned StreamReader.
func (t *PodLogTool) InvokeAsStream(ctx context.Context, params *PodLogParams) (*schema.StreamReader[string], error) {
	c, err := t.validate(params)
	if err != nil {
		return nil, err
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

	re := regexp.MustCompile(params.FilterPattern)

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer podLogs.Close()
		defer sw.Close()

		scanner := bufio.NewScanner(podLogs)
		for scanner.Scan() {
			if re.MatchString(scanner.Text()) {
				if closed := sw.Send(scanner.Text(), nil); closed {
					return
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			sw.Send("", errors.Wrap(scanErr, "error reading pod log stream"))
		}
	}()

	return sr, nil
}

// NewPodLogTool creates a new PodLogTool that supports both invokable (non-streaming) and
// streamable (streaming) modes. The returned value satisfies both tool.InvokableTool and
// tool.StreamableTool and can be used in either mode by the eino ToolsNode.
func NewPodLogTool(ctx context.Context, configs Configs) (*PodLogTool, error) {
	podLogTool := &PodLogTool{
		knownClusters: configs.GetClusterNames(),
	}

	clients, err := BuildClientSets(configs, nil)
	if err != nil {
		return nil, err
	}
	podLogTool.clients = clients

	// Wire the non-streaming (invokable) path.
	invokable, err := utils.InferTool("kubernetes_pod_logs", podLogDescription, podLogTool.Invoke)
	if err != nil {
		return nil, err
	}
	podLogTool.InvokableTool = invokable

	// Wire the streaming path.
	streamable, err := utils.InferStreamTool("kubernetes_pod_logs", podLogDescription, podLogTool.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	podLogTool.StreamableTool = streamable

	return podLogTool, nil
}
