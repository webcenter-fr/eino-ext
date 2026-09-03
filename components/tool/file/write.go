package file

import (
	"context"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const fileWriteDescription = `
** General Purpose **
It creates or overwrites a file in the session-scoped temporary directory.

** Output **
It returns a JSON object with the file path, bytes written, and whether the
operation was an append or overwrite.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Parent directories are created automatically if they don't exist.
- By default, the file is overwritten (truncated). Set Append=true to append.
- Content size is limited by the configured max write size (default 10MB).
`

// WriteParams holds the parameters for the file_write tool.
type WriteParams struct {
	Path    string `json:"path"    validate:"required" jsonschema:"(required) Relative file path inside the session directory."`
	Content string `json:"content" validate:"required" jsonschema:"(required) File content to write."`
	Append  bool   `json:"append,omitempty" jsonschema:"(optional) If true, append content to the existing file instead of overwriting."`
}

// WriteOutput is the JSON result returned by the file_write tool.
type WriteOutput struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
	Mode  string `json:"mode"` // "overwrite" or "append"
}

// WriteTool is the eino tool for the file_write operation within the
// session-scoped directory. It implements tool.InvokableTool.
type WriteTool struct {
	cfg       *Config
	invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*WriteTool)(nil)

// Invoke validates the parameters and performs the write or append to operation.
func (t *WriteTool) Invoke(ctx context.Context, params *WriteParams) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	maxWrite := t.cfg.MaxWriteBytes
	if maxWrite == 0 {
		maxWrite = fileutil.DefaultMaxWriteBytes
	}
	if len(params.Content) > maxWrite {
		return "", errors.Errorf("parameter 'content' size %d bytes exceeds the maximum %d bytes", len(params.Content), maxWrite)
	}

	safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, true)
	if err != nil {
		return "", err
	}

	// Reject if target exists and is a directory or symlink.
	if fi, statErr := os.Lstat(safePath); statErr == nil {
		if fi.IsDir() {
			return "", errors.Errorf("path %q is a directory; cannot write a file over a directory", params.Path)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("path %q is a symlink; symlinks are not allowed", params.Path)
		}
	}

	mode := "overwrite"
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if params.Append {
		mode = "append"
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}

	f, err := os.OpenFile(safePath, flag, 0o644)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open file %q for writing", params.Path)
	}
	defer func() { _ = f.Close() }()

	n, err := f.WriteString(params.Content)
	if err != nil {
		return "", errors.Wrapf(err, "failed to write to file %q", params.Path)
	}

	output := &WriteOutput{
		Path:  params.Path,
		Bytes: n,
		Mode:  mode,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewWriteTool creates the file_write tool against the given configuration.
func NewWriteTool(ctx context.Context, cfg *Config) (*WriteTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}
	writeTool := &WriteTool{cfg: cfg}
	t, err := utils.InferTool("file_write", fileWriteDescription, writeTool.Invoke)
	if err != nil {
		return nil, err
	}
	writeTool.invokable = t
	return writeTool, nil
}

// Info implements tool.InvokableTool.
func (t *WriteTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *WriteTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}
