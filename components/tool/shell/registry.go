package shell

import (
	"context"
	_ "embed"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	safetymw "github.com/webcenter-fr/eino-ext/components/middleware/safety"
	daggerlib "github.com/webcenter-fr/eino-ext/libs/toolkit/dagger"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/profile"
	toolkitsafety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

//go:embed prompts/shell_description.md
var shellDescription string

var (
	_ tool.InvokableTool  = (*ShellTool)(nil)
	_ tool.StreamableTool = (*ShellTool)(nil)
)

func NewShellTool(ctx context.Context, cfg *Config) (*ShellTool, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = defaultExecTimeout
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	blocklistPatterns := toolkitsafety.DefaultCommandBlocklist
	if len(cfg.Blocklist) > 0 {
		blocklistPatterns = cfg.Blocklist
	}

	bl, err := toolkitsafety.CompileBlocklist(blocklistPatterns)
	if err != nil {
		return nil, err
	}

	daggerCfg := &daggerlib.EngineConfig{
		RegistryAuth: cfg.RegistryAuth,
		Workdir:      cfg.Workdir,
	}
	client, err := daggerlib.NewClient(ctx, daggerCfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Dagger client")
	}

	st := &ShellTool{
		client:    client,
		cfg:       cfg,
		blocklist: bl,
		resolver:  profile.NewResolver(),
		sessions:  newSessionManager(client, cfg),
	}

	invokable, err := utils.InferTool("shell_exec", shellDescription, st.Invoke)
	if err != nil {
		client.Close()
		return nil, errors.Wrap(err, "failed to create invokable tool")
	}
	st.invokable = invokable

	streamable, err := utils.InferStreamTool("shell_exec", shellDescription, st.InvokeAsStream)
	if err != nil {
		client.Close()
		return nil, errors.Wrap(err, "failed to create streamable tool")
	}
	st.streamable = streamable

	return st, nil
}

func NewShellToolsForProfiles(ctx context.Context, cfg *Config) (map[string]*ShellTool, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	r := profile.NewResolver()
	profiles, err := r.Resolve(ctx, cfg.Workdir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve profiles")
	}

	tools := make(map[string]*ShellTool)
	for _, p := range profiles {
		profileCfg := *cfg
		profileCfg.BaseImage = p.BaseImage
		t, err := NewShellTool(ctx, &profileCfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create shell tool for profile %q", p.Name)
		}
		tools[p.Name] = t
	}

	return tools, nil
}

func WriteToolNames() []string {
	return []string{"shell_exec"}
}

func NewAllToolsWithSafety(ctx context.Context, cfg *Config, safetyCfg *safetymw.Config) ([]tool.InvokableTool, *safetymw.Middleware, error) {
	shellTool, err := NewShellTool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	tools := []tool.InvokableTool{shellTool}

	if safetyCfg == nil {
		safetyCfg = &safetymw.Config{}
	}
	if len(safetyCfg.WriteToolNames) == 0 {
		safetyCfg.WriteToolNames = WriteToolNames()
	}

	mw, err := safetymw.New(safetyCfg)
	if err != nil {
		return nil, nil, err
	}

	return tools, mw, nil
}
