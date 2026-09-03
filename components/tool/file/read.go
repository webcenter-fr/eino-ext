package file

import (
	"context"
	"fmt"
	"io"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const fileReadDescription = `
** General Purpose **
It reads the contents of a file from the session-scoped temporary directory.

** Output **
It returns a JSON object with the file path, content (or a line range), byte size,
and a truncated flag if the file exceeds the read limit.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Binary files are detected and refused.
- Files larger than the configured max read size (default 1MB) are truncated with a note.
- Use StartLine and EndLine (1-indexed) to read a specific line range.
`

// ReadParams holds the parameters for the file_read tool.
type ReadParams struct {
	Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file path inside the session directory."`
	StartLine int    `json:"startLine,omitempty" jsonschema:"(optional) 1-indexed first line to read. 0 = start from beginning."`
	EndLine   int    `json:"endLine,omitempty"   jsonschema:"(optional) 1-indexed last line to read. 0 = read to end."`
}

// ReadOutput is the JSON result returned by the file_read tool.
type ReadOutput struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// ReadTool is the eino tool for the file_read operation within the
// session-scoped directory. It implements tool.InvokableTool.
type ReadTool struct {
	cfg       *Config
	invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*ReadTool)(nil)

// Invoke validates the parameters and performs the read operation.
func (t *ReadTool) Invoke(ctx context.Context, params *ReadParams) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	if params.StartLine > 0 && params.EndLine > 0 && params.StartLine > params.EndLine {
		return "", errors.Errorf("parameter 'startLine' (%d) must be <= 'endLine' (%d)", params.StartLine, params.EndLine)
	}

	safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, false)
	if err != nil {
		return "", err
	}

	if fi, statErr := os.Lstat(safePath); statErr == nil {
		if fi.IsDir() {
			return "", errors.Errorf("path %q is a directory, not a file", params.Path)
		}
	}

	f, err := os.Open(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "file %q not found", params.Path)
		}
		return "", errors.Wrapf(err, "failed to read file %q", params.Path)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", errors.Wrapf(err, "failed to stat file %q", params.Path)
	}

	maxRead := t.cfg.MaxReadBytes
	if maxRead == 0 {
		maxRead = fileutil.DefaultMaxReadBytes
	}

	// Cap the read at maxRead bytes so memory usage stays bounded no matter
	// how large the file is; reading the whole file first (e.g. os.ReadFile)
	// would allocate for the entire file before truncating (CWE-400/CWE-770).
	data, err := io.ReadAll(io.LimitReader(f, int64(maxRead)))
	if err != nil {
		return "", errors.Wrapf(err, "failed to read file %q", params.Path)
	}

	if fileutil.IsBinary(data) {
		return "", errors.Errorf("file %q appears to be binary; refusing to read", params.Path)
	}

	output := &ReadOutput{
		Path:  params.Path,
		Bytes: len(data),
	}

	// The file is truncated only when the read hit the cap while the file on
	// disk is actually larger (a file of exactly maxRead bytes is returned
	// in full).
	if len(data) == maxRead && fi.Size() > int64(maxRead) {
		output.Truncated = true
		output.Note = fmt.Sprintf("file truncated to %d bytes (original size: %d bytes)", maxRead, fi.Size())
	}

	output.Content = string(data)

	if params.StartLine > 0 || params.EndLine > 0 {
		content, actualStart, actualEnd := fileutil.ApplyLineRange(output.Content, params.StartLine, params.EndLine)
		output.Content = content
		output.StartLine = actualStart
		output.EndLine = actualEnd
		output.Bytes = len(output.Content)
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewReadTool creates the file_read tool against the given configuration.
func NewReadTool(ctx context.Context, cfg *Config) (*ReadTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}
	readTool := &ReadTool{cfg: cfg}
	t, err := utils.InferTool("file_read", fileReadDescription, readTool.Invoke)
	if err != nil {
		return nil, err
	}
	readTool.invokable = t
	return readTool, nil
}

// Info implements tool.InvokableTool.
func (t *ReadTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *ReadTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}
