// Package pipe provides an eino tool that composes a chain of sandboxed shell
// commands and registered tools into a single pipeline. Each step's stdout is
// piped into the next step's stdin (shell) or input (tool), with no LLM
// round-trip between steps. It implements both tool.InvokableTool and
// tool.StreamableTool.
package pipe

import (
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// Config holds configuration for the pipe tool.
type Config struct {
	// Shell is the Dagger sandbox configuration. The pipe constructs its own
	// *shell.Tool from it to execute shell steps.
	Shell *shell.Config `validate:"required" jsonschema:"description=Sandbox configuration for shell steps"`

	// Tools is the registry of other tools that tool steps may invoke, keyed by
	// tool name. Read tools run ungated; tools listed in WriteToolNames are
	// gated (dry-run/confirmed + host authorization) as tool steps.
	Tools map[string]tool.InvokableTool `validate:"-" json:"-" jsonschema:"-"`

	// WriteToolNames lists registered tool names that require the
	// dry-run/confirmed authorization gate when invoked as a `tool` step.
	// These names typically come from each component's WriteToolNames()
	// registry (e.g. kubernetes.WriteToolNames(), argocd.WriteToolNames()).
	// Tools not listed here run ungated.
	WriteToolNames []string `validate:"omitempty" json:"writeToolNames,omitempty" jsonschema:"description=Registered tool names that require the dry-run/confirmed authorization gate when used as tool steps"`

	// ExecutionAuthorizer gates real execution of write tool steps, mirroring
	// safety.Config.ExecutionAuthorizer. When nil, a confirmed write tool step
	// fails closed with safety.ErrExecutionNotAuthorized.
	ExecutionAuthorizer safety.ExecutionAuthorizer `validate:"-" json:"-" jsonschema:"-"`

	// AllowModelConfirmation trusts a model-supplied confirmed=true in a write
	// tool step's args without an ExecutionAuthorizer. INSECURE: only for
	// tests and non-production sandboxes.
	AllowModelConfirmation bool `validate:"omitempty" json:"allowModelConfirmation,omitempty" jsonschema:"description=Insecure escape hatch that trusts model-supplied confirmed=true for write tool steps (tests/sandboxes only)"`

	// DefaultTimeout bounds the whole pipeline; individual shell steps also
	// use it as a per-step cap.
	DefaultTimeout time.Duration `validate:"omitempty" jsonschema:"description=Default timeout for the whole pipeline"`

	// MaxOutputBytes caps the piped output after each step (0 => default).
	MaxOutputBytes int `validate:"omitempty,gte=0" jsonschema:"description=Max bytes allowed to flow between steps"`
}

// Params defines the parameters for a pipeline invocation.
type Params struct {
	Steps   []Step `json:"steps" validate:"required,min=1" jsonschema:"(required) Ordered pipeline steps"`
	Profile string `json:"profile,omitempty" validate:"omitempty" jsonschema:"(optional) Profile override for shell steps"`
	DryRun  bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the pipeline without executing"`
	Timeout string `json:"timeout,omitempty" validate:"omitempty" jsonschema:"(optional) Timeout duration string for the whole pipeline"`
}

// Step is a single pipeline stage. Exactly one of Shell or Tool must be set.
type Step struct {
	Shell *ShellStep `json:"shell,omitempty"`
	Tool  *ToolStep  `json:"tool,omitempty"`
}

// ShellStep runs a command in the Dagger sandbox with the previous step's
// stdout piped to its stdin.
type ShellStep struct {
	Command []string `json:"command" validate:"required,min=1" jsonschema:"(required) The command to execute as an array of strings"`
	Profile string   `json:"profile,omitempty" validate:"omitempty" jsonschema:"(optional) Profile override for this step"`
}

// ToolStep invokes a tool from Config.Tools. If Args is omitted/nil, the
// previous step's stdout is passed verbatim as the tool's JSON arguments
// (it must therefore already be valid JSON for the target tool).
type ToolStep struct {
	Name string         `json:"name" validate:"required" jsonschema:"(required) Name of a registered tool"`
	Args map[string]any `json:"args,omitempty" validate:"omitempty" jsonschema:"(optional) Tool arguments. If omitted, the previous step's stdout is used as the tool's raw JSON input."`
}
