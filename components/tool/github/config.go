package github

import (
	"time"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// Configs maps a named GitHub instance to its configuration.
type Configs map[string]Config

// Config represents the configuration for a GitHub instance.
type Config struct {
	// Token is the GitHub PAT or App token. Required. REDACT in all output/logs.
	Token string `validate:"required" jsonschema:"description=GitHub token (PAT or App installation token)"`

	// BaseURL for GitHub Enterprise Server. Empty = github.com.
	BaseURL string `validate:"omitempty,url" jsonschema:"description=GitHub Enterprise base URL (empty for github.com)"`

	// UploadURL for GHES uploads (releases assets). Empty = derive from BaseURL.
	UploadURL string `validate:"omitempty,url" jsonschema:"description=GHES upload URL"`

	// CloneDir is the temp folder root where repos are cloned. Set at tool creation
	// time, NOT chosen by the LLM. Required for clone/branch tools.
	CloneDir string `validate:"required" jsonschema:"description=Base directory for local clones"`

	// Timeout for API calls. Defaulted; validated as gte=1s.
	Timeout time.Duration `validate:"omitempty,gte=1000000000" jsonschema:"description=Per-request timeout"`
}

// GetConfig retrieves the configuration for a given instance name.
func (c Configs) GetConfig(instanceName string) Config {
	return c[instanceName]
}

// GetInstanceNames returns a slice of all instance names present in the Configs map.
func (c Configs) GetInstanceNames() []string {
	return toolutil.SortedKeys(c)
}
