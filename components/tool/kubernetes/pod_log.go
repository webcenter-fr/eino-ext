package kubernetes

import (
	"bufio"
	"context"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const podLogDescription = `
** General Purpose **
It gets the logs of a specific pod in a specified Kubernetes cluster.

The log lines can be filtered using a regex pattern, and the number of log lines can be limited using the maxLines parameter.

** Output **
It return a Raw string representing the logs of the pod. Each log line is separated by a newline character.

`

// PodLogParams defines the parameters for the PodLog function.
type PodLogParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pod."`
	Name          string `json:"name" validate:"required" jsonschema:"(required) The pod name."`
	Container     string `json:"container,omitempty" validate:"omitempty" jsonschema:"(optional) The container name. If not specified, logs from the first container will be returned."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
	FilterPattern string `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A Go RE2 regex applied on each log line. Only matching lines are returned. Example: 'error|panic'. Invalid regex returns an error."`
}

// PodLogTool is a tool that gets the logs of a specific pod in a specified Kubernetes cluster.
// Implements both tool.InvokableTool (blocking) and tool.StreamableTool (streaming).
type PodLogTool struct {
	base       *baseTool
	invokable  tool.InvokableTool
	streamable tool.StreamableTool
}

// validate validates the given PodLogParams and returns the correct clientset.
func (t *PodLogTool) validate(params *PodLogParams) error {
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	return validate.Struct(params)
}

// Invoke gets the logs of a pod and returns them as a single string (non-streaming).
func (t *PodLogTool) Invoke(ctx context.Context, params *PodLogParams) (string, error) {
	if err := t.validate(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return "", err
	}

	c, err := t.base.clientset(params.Cluster)
	if err != nil {
		return "", err
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

	buf := bufio.NewScanner(podLogs)
	var logs []string
	for buf.Scan() {
		if re == nil || re.MatchString(buf.Text()) {
			logs = append(logs, buf.Text())
		}
	}
	if err := buf.Err(); err != nil {
		return "", errors.Wrap(err, "error reading pod logs")
	}

	return strings.Join(logs, "\n"), nil
}

// InvokeAsStream gets the logs of a pod and returns them line-by-line as a schema.StreamReader[string].
func (t *PodLogTool) InvokeAsStream(ctx context.Context, params *PodLogParams) (*schema.StreamReader[string], error) {
	if err := t.validate(params); err != nil {
		return nil, err
	}

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return nil, err
	}

	c, err := t.base.clientset(params.Cluster)
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

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer podLogs.Close()
		defer sw.Close()

		scanner := bufio.NewScanner(podLogs)
		for scanner.Scan() {
			if re == nil || re.MatchString(scanner.Text()) {
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

// Info returns tool information by delegating to the embedded invokable tool.
func (t *PodLogTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun executes the tool in non-streaming mode.
func (t *PodLogTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}

// StreamableRun executes the tool in streaming mode.
func (t *PodLogTool) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return t.streamable.StreamableRun(ctx, args, opts...)
}

// NewPodLogTool creates a new PodLogTool that supports both invokable and streamable modes.
func NewPodLogTool(ctx context.Context, configs Configs) (*PodLogTool, error) {
	clientsets, err := BuildClientSets(configs, nil)
	if err != nil {
		return nil, err
	}

	podLogTool := &PodLogTool{
		base: &baseTool{
			configs:       configs,
			clientsets:    clientsets,
			clients:       make(map[string]client.Client),
			knownClusters: configs.GetClusterNames(),
		},
	}

	// Wire the non-streaming (invokable) path.
	invokable, err := utils.InferTool("kubernetes_pod_logs", podLogDescription, podLogTool.Invoke)
	if err != nil {
		return nil, err
	}
	podLogTool.invokable = invokable

	// Wire the streaming path.
	streamable, err := utils.InferStreamTool("kubernetes_pod_logs", podLogDescription, podLogTool.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	podLogTool.streamable = streamable

	return podLogTool, nil
}
