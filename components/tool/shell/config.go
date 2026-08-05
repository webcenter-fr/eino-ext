// Package shell provides an eino shell tool backed by the Dagger engine.
// It gives LLM agents a secure, sandboxed shell inside an OCI base image
// (golang, node, python, etc.) with shared caching across agents.
//
// The tool implements both tool.InvokableTool and tool.StreamableTool.
package shell

import (
	"regexp"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/dagger"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/profile"
)

type Config struct {
	BaseImage      string                     `validate:"omitempty" jsonschema:"description=Default OCI base image when no profile matches"`
	Profiles       map[string]profile.Profile `validate:"omitempty" jsonschema:"description=Profile overrides keyed by profile name"`
	Workdir        string                     `validate:"required" jsonschema:"description=Project workdir to mount into containers"`
	NetworkPolicy  *egress.Policy             `validate:"omitempty" jsonschema:"description=Egress network policy for container outbound access"`
	CacheKey       string                     `validate:"omitempty" jsonschema:"description=Cache volume key prefix for shared installs"`
	RegistryAuth   map[string]dagger.RegistryAuth `validate:"omitempty" jsonschema:"description=Registry auth credentials keyed by hostname"`
	Blocklist      []string                   `validate:"omitempty" jsonschema:"description=Custom command blocklist patterns (extend defaults)"`
	DefaultTimeout time.Duration              `validate:"omitempty" jsonschema:"description=Default timeout for command execution"`
}

type ShellParams struct {
	Command          []string `json:"command" validate:"required,min=1" jsonschema:"(required) The command to execute as an array of strings"`
	Profile          string   `json:"profile,omitempty" validate:"omitempty" jsonschema:"(optional) Profile name to override default container image"`
	DryRun           bool     `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the command without executing"`
	Confirmed        bool     `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute"`
	FilterPattern    string   `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A Go RE2 regex applied on each output line. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'error|panic'. Invalid regex returns an error."`
	Timeout          string   `json:"timeout,omitempty" validate:"omitempty" jsonschema:"(optional) Timeout duration string (e.g. '30s', '5m')"`
	AllowLocalNetwork *bool   `json:"allowLocalNetwork,omitempty" jsonschema:"(optional) Override to allow local network access for this call"`
}

type ShellTool struct {
	invokable  tool.InvokableTool
	streamable tool.StreamableTool
	client     *dagger.Client
	cfg        *Config
	blocklist  []*regexp.Regexp
	resolver   *profile.Resolver
	sessions   *sessionManager
}
