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
	"github.com/webcenter-fr/eino-ext/libs/toolkit/workspace"
)

// Config holds configuration for shell tool instances and profiles.
type Config struct {
	BaseImage      string                         `validate:"omitempty" jsonschema:"description=Default OCI base image when no profile matches"`
	Profiles       map[string]profile.Profile     `validate:"omitempty" jsonschema:"description=Profile overrides keyed by profile name"`
	Workdir        string                         `validate:"required_without=Workspace" jsonschema:"description=Project workdir to mount into containers (fallback when no workspace owner/repo is given)"`
	Workspace      *workspace.Layout              `validate:"omitempty" jsonschema:"description=Session workspace layout shared with the GitHub clone tools"`
	NetworkPolicy  *egress.Policy                 `validate:"omitempty" jsonschema:"description=Egress network policy for container outbound access"`
	CacheKey       string                         `validate:"omitempty" jsonschema:"description=Cache volume key prefix for shared installs"`
	RegistryAuth   map[string]dagger.RegistryAuth `validate:"omitempty" jsonschema:"description=Registry auth credentials keyed by hostname"`
	Blocklist      []string                       `validate:"omitempty" jsonschema:"description=Custom command blocklist patterns (extend defaults)"`
	DefaultTimeout time.Duration                  `validate:"omitempty" jsonschema:"description=Default timeout for command execution"`
	SessionIdleTTL time.Duration                  `validate:"omitempty,gte=0" jsonschema:"description=Idle TTL for cached session containers (default 1h)"`
}

// Params defines the parameters for shell command execution.
type Params struct {
	Command           []string `json:"command" validate:"required,min=1" jsonschema:"(required) The command to execute as an array of strings"`
	Stdin             string   `json:"stdin,omitempty" validate:"omitempty" jsonschema:"(optional) Text to pipe to the command's standard input (stdin)"`
	Profile           string   `json:"profile,omitempty" validate:"omitempty" jsonschema:"(optional) Profile name to override default container image"`
	Owner             string   `json:"owner,omitempty" validate:"omitempty" jsonschema:"(optional) Repository owner; resolves the session workspace when set with repo"`
	Repo              string   `json:"repo,omitempty" validate:"omitempty" jsonschema:"(optional) Repository name; resolves the session workspace when set with owner"`
	PersistChanges    bool     `json:"persistChanges,omitempty" jsonschema:"(optional) If true, export container /workspace changes back to the host (excluding .git). Default false: the exec is throwaway."`
	DryRun            bool     `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the command without executing"`
	FilterPattern     string   `json:"filterPattern,omitempty" validate:"omitempty" jsonschema:"(optional) A Go RE2 regex applied on each output line. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'error|panic'. Invalid regex returns an error."`
	Timeout           string   `json:"timeout,omitempty" validate:"omitempty" jsonschema:"(optional) Timeout duration string (e.g. '30s', '5m')"`
	AllowLocalNetwork *bool    `json:"allowLocalNetwork,omitempty" jsonschema:"(optional) Override to allow local network access for this call"`
}

// RawExecParams is a trusted, gate-free shell execution request used by the
// pipe tool (and future compositors). It applies the blocklist and resolves the
// profile, but skips dry-run and output filtering — the composing caller owns
// those. It is NOT intended for direct end-user invocation.
type RawExecParams struct {
	Command        []string      // required, non-empty
	Profile        string        // optional; "" means auto-detect
	Stdin          string        // optional text piped to the command
	Timeout        time.Duration // <= 0 falls back to Config.DefaultTimeout / defaultExecTimeout
	Owner          string        // optional; resolves the session workspace when set with Repo
	Repo           string        // optional; resolves the session workspace when set with Owner
	PersistChanges bool          // optional; export container changes back to the host
}

// Tool is an eino tool for executing shell commands.
type Tool struct {
	invokable  tool.InvokableTool
	streamable tool.StreamableTool
	client     *dagger.Client
	cfg        *Config
	blocklist  []*regexp.Regexp
	resolver   *profile.Resolver
	sessions   *sessionManager
}
