package shell

import (
	"context"
	"time"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	daggerlib "github.com/webcenter-fr/eino-ext/libs/toolkit/dagger"
)

const checkTimeout = 15 * time.Second

// Check performs a health check against shell configurations.
func Check(ctx context.Context, cfg *Config) checkup.Results {
	if cfg == nil {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     "no shell config provided",
		}}
	}

	if cfg.Workdir == "" {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     "workdir is required",
		}}
	}

	daggerCfg := &daggerlib.EngineConfig{
		RegistryAuth: cfg.RegistryAuth,
		Workdir:      cfg.Workdir,
	}

	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	client, err := daggerlib.NewClient(checkCtx, daggerCfg)
	if err != nil {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}
	defer func() { _ = client.Close() }()

	version, _ := client.Version(checkCtx)

	cont, err := client.Container(checkCtx, "alpine:3.20")
	if err != nil {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	execCont := cont.WithExec([]string{"echo", "ok"})
	synced, err := execCont.Sync(checkCtx)
	if err != nil {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	stdout, err := synced.Stdout(checkCtx)
	if err != nil {
		return checkup.Results{{
			Component: "shell_exec",
			Status:    checkup.StatusError,
			Error:     err.Error(),
		}}
	}

	msg := "Dagger engine reachable, trivial exec succeeded"
	if version != "" {
		msg += ", engine version: " + version
	}

	return checkup.Results{{
		Component: "shell_exec",
		Status:    checkup.StatusOK,
		Message:   msg + ", stdout: " + stdout,
	}}
}
