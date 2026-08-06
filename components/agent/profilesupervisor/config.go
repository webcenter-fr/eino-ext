// Package profilesupervisor provides a profile supervisor agent factory that
// dynamically selects, at runtime, the sub-agent whose shell sandbox uses the
// right OCI base image for the task.
package profilesupervisor

import (
	_ "embed"

	"github.com/cloudwego/eino/components/model"

	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/profile"
)

//go:embed prompts/supervisor_system.md
var defaultSupervisorPrompt string

// SupervisorConfig holds configuration for the profile supervisor agent.
type SupervisorConfig struct {
	Model         model.BaseChatModel `validate:"required" jsonschema:"description=The LLM model for the supervisor agent"`
	Workdir       string              `validate:"required" jsonschema:"description=Project workdir"`
	NetworkPolicy *egress.Policy      `validate:"omitempty" jsonschema:"description=Egress network policy"`
	Profiles      []profile.Profile   `validate:"omitempty" jsonschema:"description=Profile overrides (optional, auto-detected if empty)"`
	Resolver      *profile.Resolver   `validate:"omitempty" jsonschema:"description=Profile resolver (optional, uses defaults if nil)"`
	SafetyCfg     *safety.Config      `validate:"omitempty" jsonschema:"description=Safety middleware configuration"`
	SystemPrompt  string              `validate:"omitempty" jsonschema:"description=Supervisor system prompt (uses default if empty)"`
}
