package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const podExecDescription = `
** General Purpose **
It executes a command in a specific pod in a specified Kubernetes cluster.

The command output can be filtered using a regex pattern, and the number of output lines can be limited using the maxLines parameter.

** IMPORTANT RULES **
Never use this tool to execute commands that may have side effects any where, such as creating, modifying or deleting resources. It should only be used for read-only commands, such as "cat /etc/os-release" or "ls /app". Always ensure that the command being executed does not violate the security policies of the cluster.

Additionally, commands matching a known destructive pattern (e.g., 'rm', 'kill', 'shutdown') are automatically blocked.

** Output **
It return a Raw string representing the command output of the pod. Each output line is separated by a newline character.

`

// PodExecParams defines the parameters for the PodExec function.
type PodExecParams struct {
	Cluster       string   `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Namespace     string   `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pod."`
	Name          string   `json:"name" validate:"required" jsonschema:"(required) The pod name."`
	Container     string   `json:"container,omitempty" validate:"omitempty" jsonschema:"(optional) The container name. If not specified, the command will be executed in the first container."`
	Command       []string `json:"command" validate:"required,min=1" jsonschema:"(required) The command to execute as an array of strings. Example: [\"ls\", \"-la\", \"/app\"]."`
	MaxLines      int64    `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of output lines to return. Default to 100."`
	FilterPattern string   `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A Go RE2 regex applied on each output line. Only matching lines are returned. Example: 'error|panic'. Invalid regex returns an error."`
	DryRun        bool     `json:"dryRun,omitempty" jsonschema:"(optional) Does not apply to exec; exec requires confirmed=true directly. Set this with caution."`
	Confirmed     bool     `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the command. Set this after ensuring the command is safe and read-only."`
}

// PodExecTool is a tool that executes a command in a specific pod in a specified Kubernetes cluster.
// Implements both tool.InvokableTool (blocking) and tool.StreamableTool (streaming).
type PodExecTool struct {
	base       *baseTool
	invokable  tool.InvokableTool
	streamable tool.StreamableTool
	blocklist  []*regexp.Regexp
}

var _ *rest.Config // ensure import is not removed

// defaultBlocklist contains regex patterns for destructive commands that are always blocked.
// Uses word boundaries to catch variations like /bin/rm, ./rm, etc.
var defaultBlocklist = []string{
	`\brm\b`,
	`\brmdir\b`,
	`\bkill\b`,
	`\bkillall\b`,
	`\bpkill\b`,
	`\bshutdown\b`,
	`\breboot\b`,
	`\bhalt\b`,
	`\bpoweroff\b`,
	`\bdd\b`,
	`\bmkfs\b`,
	`\bmkswap\b`,
	`\bchmod\s+.*-R\s+/`,
	`\bchown\s+.*-R\s+/`,
	`\bchroot\b`,
	`\binsmod\b`,
	`\bmodprobe\b`,
	`\biptables\b`,
	`\bsystemctl\s+stop\b`,
	`\bsystemctl\s+disable\b`,
	`\bsystemctl\s+mask\b`,
	`>.*/dev/`,
}

func compileBlocklist(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid blocklist pattern %q", p)
		}
		result = append(result, re)
	}
	return result, nil
}

// checkBlocklist validates the command against the configured blocklist patterns.
func (t *PodExecTool) checkBlocklist(command []string) error {
	cmdStr := strings.Join(command, " ")
	for _, re := range t.blocklist {
		if re.MatchString(cmdStr) {
			return errors.Errorf("command %q is blocked by security policy (matches blocklist pattern %q)", cmdStr, re.String())
		}
	}
	return nil
}

// dryRunPreview returns a human-readable preview of the exec command that would
// be run, without actually executing it.
func (t *PodExecTool) dryRunPreview(params *PodExecParams) string {
	container := params.Container
	if container == "" {
		container = "(first container)"
	}
	return fmt.Sprintf(
		`{"dryRun": true, "cluster": %q, "namespace": %q, "pod": %q, "container": %q, "command": %v}`,
		params.Cluster, params.Namespace, params.Name, container, params.Command,
	)
}

// Invoke executes a command in a pod and returns the output as a single string (non-streaming).
func (t *PodExecTool) Invoke(ctx context.Context, params *PodExecParams) (string, error) {
	// Validate params and check blocklist (applies to both dry-run and real exec).
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	if err := validate.Struct(params); err != nil {
		return "", err
	}
	if err := t.checkBlocklist(params.Command); err != nil {
		return "", err
	}

	// Dry-run: return a preview without executing.
	if params.DryRun {
		return t.dryRunPreview(params), nil
	}

	config, err := t.base.restConfig(params.Cluster)
	if err != nil {
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

	req := c.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(params.Name).
		Namespace(params.Namespace).
		SubResource("exec")

	parameterCodec := runtime.NewParameterCodec(scheme.Scheme)
	req.VersionedParams(&corev1.PodExecOptions{
		Command:   params.Command,
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
		if re == nil || re.MatchString(bufStdout.Text()) {
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
	// Validate params and check blocklist (applies to both dry-run and real exec).
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	if err := validate.Struct(params); err != nil {
		return nil, err
	}
	if err := t.checkBlocklist(params.Command); err != nil {
		return nil, err
	}

	// Dry-run: return a preview stream without executing.
	if params.DryRun {
		sr, sw := schema.Pipe[string](1)
		sw.Send(t.dryRunPreview(params), nil)
		sw.Close()
		return sr, nil
	}

	config, err := t.base.restConfig(params.Cluster)
	if err != nil {
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

	req := c.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(params.Name).
		Namespace(params.Namespace).
		SubResource("exec")

	parameterCodec := runtime.NewParameterCodec(scheme.Scheme)
	req.VersionedParams(&corev1.PodExecOptions{
		Command:   params.Command,
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

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	go func() {
		defer stdoutW.Close()
		defer stderrW.Close()
		execErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:  nil,
			Stdout: stdoutW,
			Stderr: stderrW,
			Tty:    false,
		})
		if execErr != nil {
			stdoutW.CloseWithError(execErr)
			stderrW.CloseWithError(execErr)
		}
	}()

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		scannerStdout := bufio.NewScanner(stdoutR)
		for scannerStdout.Scan() {
			line := scannerStdout.Text()
			if re == nil || re.MatchString(line) {
				if closed := sw.Send(line, nil); closed {
					return
				}
			}
		}
		if scanErr := scannerStdout.Err(); scanErr != nil {
			sw.Send("", errors.Wrap(scanErr, "error reading pod log stream"))
		}

		stderrScanner := bufio.NewScanner(stderrR)
		for stderrScanner.Scan() {
			if closed := sw.Send(stderrScanner.Text(), nil); closed {
				return
			}
		}
	}()

	return sr, nil
}

// Info returns tool information by delegating to the embedded invokable tool.
func (t *PodExecTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun executes the tool in non-streaming mode.
func (t *PodExecTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}

// StreamableRun executes the tool in streaming mode.
func (t *PodExecTool) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return t.streamable.StreamableRun(ctx, args, opts...)
}

// NewPodExecTool creates a new PodExecTool that supports both invokable and streamable modes.
func NewPodExecTool(ctx context.Context, configs Configs) (*PodExecTool, error) {
	clientsets, err := BuildClientSets(configs, nil)
	if err != nil {
		return nil, err
	}

	bl, err := compileBlocklist(defaultBlocklist)
	if err != nil {
		return nil, err
	}

	podExecTool := &PodExecTool{
		base: &baseTool{
			configs:       configs,
			clientsets:    clientsets,
			knownClusters: configs.GetClusterNames(),
		},
		blocklist: bl,
	}

	// Wire the non-streaming (invokable) path.
	invokable, err := utils.InferTool("kubernetes_pod_exec", podExecDescription, podExecTool.Invoke)
	if err != nil {
		return nil, err
	}
	podExecTool.invokable = invokable

	// Wire the streaming path.
	streamable, err := utils.InferStreamTool("kubernetes_pod_exec", podExecDescription, podExecTool.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	podExecTool.streamable = streamable

	return podExecTool, nil
}
