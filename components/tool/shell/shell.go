package shell

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const defaultExecTimeout = 5 * time.Minute

func (t *Tool) resolveProfile(ctx context.Context, profileName string) (string, string, error) {
	if profileName == "" {
		detected, err := t.resolver.Resolve(ctx, t.cfg.Workdir)
		if err != nil {
			return "", "", errors.Wrap(err, "failed to detect project profile")
		}
		if len(detected) > 0 {
			profileName = detected[0].Name
		} else {
			profileName = "default"
		}
	}

	baseImage, err := t.resolveImage(profileName)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to resolve base image")
	}

	return profileName, baseImage, nil
}

func (t *Tool) resolveImage(profileName string) (string, error) {
	if t.cfg.Profiles != nil {
		if p, ok := t.cfg.Profiles[profileName]; ok {
			return p.BaseImage, nil
		}
	}

	profiles, err := t.resolver.Resolve(context.Background(), t.cfg.Workdir)
	if err != nil {
		return "", err
	}

	for _, p := range profiles {
		if p.Name == profileName {
			return p.BaseImage, nil
		}
	}

	if t.cfg.BaseImage != "" {
		return t.cfg.BaseImage, nil
	}

	return "alpine:3.20", nil
}

func (t *Tool) getTimeout(params *Params) time.Duration {
	if params.Timeout != "" {
		if d, err := time.ParseDuration(params.Timeout); err == nil {
			return d
		}
	}
	if t.cfg.DefaultTimeout > 0 {
		return t.cfg.DefaultTimeout
	}
	return defaultExecTimeout
}

func (t *Tool) dryRunPreview(params *Params) string {
	profile := params.Profile
	if profile == "" {
		profile = "(auto-detect)"
	}
	return fmt.Sprintf(
		`{"dryRun": true, "command": %v, "profile": %q}`,
		params.Command, profile,
	)
}

// Invoke runs the shell command and returns its output.
func (t *Tool) Invoke(ctx context.Context, params *Params) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	if err := toolkitsafety.CheckBlocklist(t.blocklist, params.Command); err != nil {
		return "", err
	}

	if params.DryRun {
		return t.dryRunPreview(params), nil
	}

	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	profileName, baseImage, err := t.resolveProfile(ctx, params.Profile)
	if err != nil {
		return "", err
	}

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return "", err
	}

	timeout := t.getTimeout(params)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ses, err := t.sessions.getOrCreate(execCtx, profileName, baseImage)
	if err != nil {
		return "", errors.Wrap(err, "failed to get session container")
	}

	stdout, stderr, exitCode, err := t.sessions.exec(execCtx, ses, params.Command)
	if err != nil {
		return "", err
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := scanner.Text()
		if re == nil || re.MatchString(line) {
			lines = append(lines, line)
		}
	}

	if stderr != "" {
		if exitCode != 0 {
			lines = append(lines, fmt.Sprintf("[exit code: %d]", exitCode))
		}
		lines = append(lines, stderr)
	} else if exitCode != 0 {
		lines = append(lines, fmt.Sprintf("[exit code: %d]", exitCode))
	}

	return strings.Join(lines, "\n"), nil
}

// InvokeAsStream runs the shell command and returns a stream reader.
func (t *Tool) InvokeAsStream(ctx context.Context, params *Params) (*schema.StreamReader[string], error) {
	if err := validate.Struct(params); err != nil {
		return nil, err
	}

	if err := toolkitsafety.CheckBlocklist(t.blocklist, params.Command); err != nil {
		return nil, err
	}

	if params.DryRun {
		sr, sw := schema.Pipe[string](1)
		sw.Send(t.dryRunPreview(params), nil)
		sw.Close()
		return sr, nil
	}

	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return nil, err
	}

	profileName, baseImage, err := t.resolveProfile(ctx, params.Profile)
	if err != nil {
		return nil, err
	}

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return nil, err
	}

	timeout := t.getTimeout(params)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ses, err := t.sessions.getOrCreate(execCtx, profileName, baseImage)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get session container")
	}

	stdout, stderr, exitCode, err := t.sessions.exec(execCtx, ses, params.Command)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		scanner := bufio.NewScanner(strings.NewReader(stdout))
		for scanner.Scan() {
			line := scanner.Text()
			if re == nil || re.MatchString(line) {
				if closed := sw.Send(line, nil); closed {
					return
				}
			}
		}

		if stderr != "" {
			scannerStderr := bufio.NewScanner(strings.NewReader(stderr))
			for scannerStderr.Scan() {
				if closed := sw.Send(scannerStderr.Text(), nil); closed {
					return
				}
			}
		}

		if exitCode != 0 {
			sw.Send(fmt.Sprintf("[exit code: %d]", exitCode), nil)
		}
	}()

	return sr, nil
}

// Info returns metadata about the shell tool.
func (t *Tool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements the eino InvokableTool interface.
func (t *Tool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}

// StreamableRun implements the eino StreamableTool interface.
func (t *Tool) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return t.streamable.StreamableRun(ctx, args, opts...)
}
