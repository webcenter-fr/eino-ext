package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/go-playground/validator/v10"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const podExecDescription = `
** General Purpose **
It executes a command in a specific pod in a specified Kubernetes cluster.

The command output can be filtered using a regex pattern, and the number of output lines can be limited using the maxLines parameter.

** Output **
It return a Raw string representing the command output of the pod. Each output line is separated by a newline character.

`

// PodExecParams defines the parameters for the PodExec function, which executes a command in a specific pod in a specified Kubernetes cluster. It includes the cluster name, namespace, and pod name.
type PodExecParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pod."`
	Name          string `json:"name" validate:"required" jsonschema:"(required) The pod name."`
	Container     string `json:"container,omitempty" validate:"omitempty" jsonschema:"(optional) The container name. If not specified, the command will be executed in the first container."`
	Command       string `json:"command" validate:"required" jsonschema:"(required) The command to execute in the pod."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of output lines to return. Default to 100."`
	FilterPattern string `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A regex pattern to filter output lines. Only lines matching the pattern will be returned."`
}

// PodExecTool is a tool that executes a command in a specific pod in a specified Kubernetes cluster.
// It implements both tool.InvokableTool (blocking, returns full command output as string) and
// tool.StreamableTool (streaming, returns command output lines as a schema.StreamReader).
type PodExecTool struct {
	clients       map[string]*kubernetes.Clientset
	configs       Configs
	knownClusters []string

	tool.InvokableTool
	tool.StreamableTool
}

// validate validates the given PodExecParams.
func (t *PodExecTool) validate(params *PodExecParams) (*kubernetes.Clientset, *rest.Config, error) {
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	v := validator.New()
	if err := v.Struct(params); err != nil {
		return nil, nil, errors.Wrap(err, "invalid parameters for PodExecTool")
	}

	c, ok := t.clients[params.Cluster]
	if !ok {
		return nil, nil, errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s", params.Cluster, strings.Join(t.knownClusters, ", "))
	}

	return c, t.configs.GetConfig(params.Cluster), nil
}

// Invoke executes a command in a pod and returns the output as a single string (non-streaming).
func (t *PodExecTool) Invoke(ctx context.Context, params *PodExecParams) (string, error) {
	c, config, err := t.validate(params)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(params.FilterPattern)

	req := c.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(params.Name).
		Namespace(params.Namespace).
		SubResource("exec")

	parameterCodec := runtime.NewParameterCodec(scheme.Scheme)
	req.VersionedParams(&corev1.PodExecOptions{
		Command:   strings.Fields(params.Command),
		Container: params.Container,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", errors.Wrap(err, "failed to create SPDY executor")
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return "", errors.Wrap(err, "error in Stream")
	}

	bufStdout := bufio.NewScanner(&stdout)
	var logs []string
	for bufStdout.Scan() {
		if re.MatchString(bufStdout.Text()) {
			logs = append(logs, bufStdout.Text())
		}
	}
	if err := bufStdout.Err(); err != nil {
		return "", errors.Wrap(err, "error reading pod logs")
	}

	if stderr.Len() > 0 {
		logs = append(logs, stderr.String())
	}

	return strings.Join(logs, "\n"), nil
}

// InvokeAsStream executes a command in a pod and returns the output line-by-line as a schema.StreamReader[string].
func (t *PodExecTool) InvokeAsStream(ctx context.Context, params *PodExecParams) (*schema.StreamReader[string], error) {
	c, config, err := t.validate(params)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(params.FilterPattern)

	req := c.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(params.Name).
		Namespace(params.Namespace).
		SubResource("exec")

	parameterCodec := runtime.NewParameterCodec(scheme.Scheme)
	req.VersionedParams(&corev1.PodExecOptions{
		Command:   strings.Fields(params.Command),
		Container: params.Container,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, errors.Wrap(err, "failed to create SPDY executor")
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error in Stream")
	}

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		scannerStdout := bufio.NewScanner(&stdout)
		for scannerStdout.Scan() {
			if re.MatchString(scannerStdout.Text()) {
				if closed := sw.Send(scannerStdout.Text(), nil); closed {
					return
				}
			}
		}
		if scanErr := scannerStdout.Err(); scanErr != nil {
			sw.Send("", errors.Wrap(scanErr, "error reading pod log stream"))
		}

		if stderr.Len() > 0 {
			if closed := sw.Send(stderr.String(), nil); closed {
				return
			}
		}
	}()

	return sr, nil
}

// NewPodExecTool creates a new PodExecTool that supports both invokable (non-streaming) and
// streamable (streaming) modes. The returned value satisfies both tool.InvokableTool and
// tool.StreamableTool and can be used in either mode by the eino ToolsNode.
func NewPodExecTool(ctx context.Context, configs Configs) (*PodExecTool, error) {
	podExecTool := &PodExecTool{
		knownClusters: configs.GetClusterNames(),
		configs:       configs,
	}

	clients, err := BuildClientSets(configs, nil)
	if err != nil {
		return nil, err
	}
	podExecTool.clients = clients

	// Wire the non-streaming (invokable) path.
	invokable, err := utils.InferTool("kubernetes_pod_exec", podExecDescription, podExecTool.Invoke)
	if err != nil {
		return nil, err
	}
	podExecTool.InvokableTool = invokable

	// Wire the streaming path.
	streamable, err := utils.InferStreamTool("kubernetes_pod_exec", podExecDescription, podExecTool.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	podExecTool.StreamableTool = streamable

	return podExecTool, nil
}
