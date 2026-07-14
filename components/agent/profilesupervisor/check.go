package profilesupervisor

import (
	"context"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func Check(ctx context.Context, cfg *SupervisorConfig) checkup.Results {
	if cfg == nil {
		return checkup.Results{{
			Component: "profile_supervisor",
			Status:    checkup.StatusError,
			Error:     "no supervisor config provided",
		}}
	}

	if cfg.Workdir == "" {
		return checkup.Results{{
			Component: "profile_supervisor",
			Status:    checkup.StatusError,
			Error:     "workdir is required",
		}}
	}

	if cfg.Model == nil {
		return checkup.Results{{
			Component: "profile_supervisor",
			Status:    checkup.StatusError,
			Error:     "model is required",
		}}
	}

	shellCfg := &shell.Config{
		Workdir:       cfg.Workdir,
		NetworkPolicy: cfg.NetworkPolicy,
	}

	return shell.Check(ctx, shellCfg)
}
