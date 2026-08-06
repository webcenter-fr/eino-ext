package profilesupervisor

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/profile"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// NewProfileSupervisor creates a new profile supervisor agent.
func NewProfileSupervisor(ctx context.Context, cfg *SupervisorConfig) (*adk.ChatModelAgent, error) {
	if cfg == nil {
		return nil, errors.New("supervisor config is nil")
	}
	if cfg.Resolver == nil {
		cfg.Resolver = profile.NewResolver()
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSupervisorPrompt
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	var profiles []profile.Profile
	if len(cfg.Profiles) > 0 {
		profiles = cfg.Profiles
	} else {
		detected, err := cfg.Resolver.Resolve(ctx, cfg.Workdir)
		if err != nil {
			return nil, errors.Wrap(err, "failed to resolve profiles")
		}
		profiles = detected
	}

	if len(profiles) == 0 {
		return nil, errors.New("no profiles available (none detected and none provided)")
	}

	agentTools := make([]tool.BaseTool, 0, len(profiles))

	for _, p := range profiles {
		shellCfg := &shell.Config{
			BaseImage:     p.BaseImage,
			Workdir:       cfg.Workdir,
			NetworkPolicy: cfg.NetworkPolicy,
		}

		shellTool, err := shell.NewShellTool(ctx, shellCfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create shell tool for profile %q", p.Name)
		}

		var handlers []adk.ChatModelAgentMiddleware
		if cfg.SafetyCfg != nil {
			safetyCfg := *cfg.SafetyCfg
			if len(safetyCfg.WriteToolNames) == 0 {
				safetyCfg.WriteToolNames = shell.WriteToolNames()
			}
			mw, mwErr := safety.New(&safetyCfg)
			if mwErr != nil {
				return nil, errors.Wrapf(mwErr, "failed to create safety middleware for profile %q", p.Name)
			}
			handlers = append(handlers, mw)
		}

		subAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name:        fmt.Sprintf("shell_%s", p.Name),
			Description: fmt.Sprintf("Shell sandbox for %s development (%s)", p.Name, p.BaseImage),
			Instruction: p.SystemPrompt,
			Model:       cfg.Model,
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{
					Tools: []tool.BaseTool{shellTool},
				},
			},
			Handlers: handlers,
		})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create sub-agent for profile %q", p.Name)
		}

		agentTool := adk.NewAgentTool(ctx, subAgent)
		agentTools = append(agentTools, agentTool)
	}

	supervisorHandlers := []adk.ChatModelAgentMiddleware{}
	if cfg.SafetyCfg != nil {
		mw, err := safety.New(cfg.SafetyCfg)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create supervisor safety middleware")
		}
		supervisorHandlers = append(supervisorHandlers, mw)
	}

	supervisor, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "profile_supervisor",
		Description: "Supervisor that dynamically selects the right language-specific sub-agent for each task",
		Instruction: cfg.SystemPrompt,
		Model:       cfg.Model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: agentTools,
			},
			EmitInternalEvents: true,
		},
		Handlers: supervisorHandlers,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create supervisor agent")
	}

	return supervisor, nil
}
