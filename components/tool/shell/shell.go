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

	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/workspace"
)

const defaultExecTimeout = 5 * time.Minute

// baseWorkdir returns the directory used for profile detection and as the
// fallback workspace when no owner/repo is given.
func (t *Tool) baseWorkdir() string {
	if t.cfg.Workdir != "" {
		return t.cfg.Workdir
	}
	if t.cfg.Workspace != nil {
		return t.cfg.Workspace.Root
	}
	return ""
}

// resolveWorkspacePath resolves the host directory mounted at /workspace. When
// owner/repo are set it uses the session workspace layout; otherwise it falls
// back to cfg.Workdir.
func (t *Tool) resolveWorkspacePath(ctx context.Context, owner, repo string) (string, error) {
	if (owner == "") != (repo == "") {
		return "", errors.New("owner and repo must be provided together")
	}
	if owner == "" && repo == "" {
		return t.baseWorkdir(), nil
	}
	if t.cfg.Workspace == nil {
		return "", errors.New("owner/repo require shell.Config.Workspace to be configured")
	}
	return t.cfg.Workspace.RepoDir(ctx, owner, repo)
}

// prepareWorkspace touches the session namespace and fails fast when the
// workspace was swept mid-session, evicting the stale container cache entry.
func (t *Tool) prepareWorkspace(ctx context.Context, key sessionKey, workspacePath string, owner, repo string) error {
	if t.cfg.Workspace == nil || owner == "" || repo == "" {
		return nil
	}
	if err := t.cfg.Workspace.Touch(ctx); err != nil {
		return err
	}
	exists, err := workspace.Exists(workspacePath)
	if err != nil {
		return err
	}
	if !exists {
		t.sessions.evict(key)
		return errors.Errorf("workspace no longer exists at %q; run github_repo_clone first", workspacePath)
	}
	return nil
}

func (t *Tool) resolveProfile(ctx context.Context, profileName string) (string, string, error) {
	if profileName == "" {
		detected, err := t.resolver.Resolve(ctx, t.baseWorkdir())
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

	profiles, err := t.resolver.Resolve(context.Background(), t.baseWorkdir())
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

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return "", err
	}

	stdout, stderr, exitCode, err := t.runInWorkspace(ctx, params.Owner, params.Repo, params.Profile, params.Command, params.Stdin, t.getTimeout(params), params.PersistChanges)
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

	re, err := filter.Compile(params.FilterPattern)
	if err != nil {
		return nil, err
	}

	stdout, stderr, exitCode, err := t.runInWorkspace(ctx, params.Owner, params.Repo, params.Profile, params.Command, params.Stdin, t.getTimeout(params), params.PersistChanges)
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

// RawExec runs a command in the sandbox and returns its raw stdout, stderr and
// exit code. It is a trusted, gate-free primitive for compositors (e.g. the
// pipe tool): the blocklist is always enforced, but dry-run and output
// filtering are the caller's responsibility. It is NOT intended for direct
// end-user invocation.
func (t *Tool) RawExec(ctx context.Context, params RawExecParams) (stdout, stderr string, exitCode int, err error) {
	if len(params.Command) == 0 {
		return "", "", -1, errors.New("raw exec requires a non-empty command")
	}

	if err := toolkitsafety.CheckBlocklist(t.blocklist, params.Command); err != nil {
		return "", "", -1, err
	}

	timeout := params.Timeout
	if timeout <= 0 {
		if t.cfg.DefaultTimeout > 0 {
			timeout = t.cfg.DefaultTimeout
		} else {
			timeout = defaultExecTimeout
		}
	}
	return t.runInWorkspace(ctx, params.Owner, params.Repo, params.Profile, params.Command, params.Stdin, timeout, params.PersistChanges)
}

// runInWorkspace resolves the workspace/profile, prepares the session, executes
// the command, and optionally exports container changes back to the host.
func (t *Tool) runInWorkspace(ctx context.Context, owner, repo, profileName string, command []string, stdin string, timeout time.Duration, persist bool) (stdout, stderr string, exitCode int, err error) {
	workspacePath, err := t.resolveWorkspacePath(ctx, owner, repo)
	if err != nil {
		return "", "", -1, err
	}

	resolvedProfile, baseImage, err := t.resolveProfile(ctx, profileName)
	if err != nil {
		return "", "", -1, err
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	key := t.sessions.key(execCtx, resolvedProfile, baseImage, owner, repo)
	if err := t.prepareWorkspace(execCtx, key, workspacePath, owner, repo); err != nil {
		return "", "", -1, err
	}

	ses, err := t.sessions.getOrCreate(execCtx, key)
	if err != nil {
		return "", "", -1, errors.Wrap(err, "failed to get session container")
	}

	stdout, stderr, exitCode, err = t.sessions.exec(execCtx, ses, workspacePath, command, stdin)
	if err != nil {
		return stdout, stderr, exitCode, err
	}

	if persist {
		if err := t.sessions.export(execCtx, ses, workspacePath); err != nil {
			return stdout, stderr, exitCode, err
		}
	}
	return stdout, stderr, exitCode, nil
}

// Close shuts down the underlying Dagger client.
func (t *Tool) Close() error {
	if t.client != nil {
		return t.client.Close()
	}
	return nil
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
