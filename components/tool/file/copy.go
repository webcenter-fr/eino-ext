package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const fileCopyDescription = `
** General Purpose **
It copies a file or directory from a source path to a destination path within
the session-scoped temporary directory.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
copy status, and (for directories) file count and total bytes.

** Important **
- Both regular files and directories can be copied.
- If the destination exists, it is overwritten (files: truncated; dirs: merged).
- Destination parent directories are created if they don't exist.
- Symlinks are always rejected.
- Symlinks inside a copied directory tree are skipped.
- The destination must not be inside the source directory.
- Path traversal attempts are rejected.
- Source and destination must differ.
`

// CopyParams holds the parameters for the file_copy tool.
type CopyParams struct {
	Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path."`
	Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path."`
}

// CopyOutput is the JSON result returned by the file_copy tool.
type CopyOutput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"` // "file" or "dir"
	Copied      bool   `json:"copied"`
	FileCount   int    `json:"fileCount,omitempty"`
	TotalBytes  int64  `json:"totalBytes,omitempty"`
}

// CopyTool is the eino tool for the file_copy operation within the
// session-scoped directory. It implements tool.InvokableTool.
type CopyTool struct {
	cfg       *Config
	invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*CopyTool)(nil)

// Invoke validates the parameters and performs the copy operation.
func (t *CopyTool) Invoke(ctx context.Context, params *CopyParams) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	if params.Source == params.Destination {
		return "", errors.Errorf("source and destination are the same path %q", params.Source)
	}

	srcSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Source, false)
	if err != nil {
		return "", err
	}

	dstSafePath, err := resolvePath(t.cfg.Workdir, ctx, params.Destination, true)
	if err != nil {
		return "", err
	}

	// Reject aliased or nested endpoints.
	cleanSrc := filepath.Clean(srcSafePath)
	cleanDst := filepath.Clean(dstSafePath)
	if cleanDst == cleanSrc {
		return "", errors.Errorf("source and destination resolve to the same path %q", params.Source)
	}
	if strings.HasPrefix(cleanDst, cleanSrc+string(filepath.Separator)) {
		return "", errors.Errorf("destination %q is inside the source directory %q", params.Destination, params.Source)
	}

	srcFi, err := os.Lstat(srcSafePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "source path %q not found", params.Source)
		}
		return "", errors.Wrapf(err, "failed to stat source path %q", params.Source)
	}

	// Reject type mismatches if destination exists.
	if dstFi, statErr := os.Lstat(dstSafePath); statErr == nil {
		if dstFi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("destination %q is a symlink; symlinks are not allowed", params.Destination)
		}
		if dstFi.IsDir() && !srcFi.IsDir() {
			return "", errors.Errorf("destination %q is a directory but source is a file", params.Destination)
		}
		if !dstFi.IsDir() && srcFi.IsDir() {
			return "", errors.Errorf("destination %q is a file but source is a directory", params.Destination)
		}
	}

	output := &CopyOutput{
		Source:      params.Source,
		Destination: params.Destination,
		Type:        "file",
		Copied:      true,
	}

	if srcFi.IsDir() {
		output.Type = "dir"
		fileCount, totalBytes, err := fileutil.CopyDir(srcSafePath, dstSafePath, false)
		if err != nil {
			return "", errors.Wrapf(err, "failed to copy directory %q to %q", params.Source, params.Destination)
		}
		output.FileCount = fileCount
		output.TotalBytes = totalBytes
	} else {
		if err := fileutil.CopyFileContents(srcSafePath, dstSafePath); err != nil {
			// Best-effort cleanup of the partial destination; the copy error
			// is what matters, so cleanup failures are ignored.
			_ = os.Remove(dstSafePath)
			return "", err
		}
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewCopyTool creates the file_copy tool against the given configuration.
func NewCopyTool(ctx context.Context, cfg *Config) (*CopyTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}
	copyTool := &CopyTool{cfg: cfg}
	t, err := utils.InferTool("file_copy", fileCopyDescription, copyTool.Invoke)
	if err != nil {
		return nil, err
	}
	copyTool.invokable = t
	return copyTool, nil
}

// Info implements tool.InvokableTool.
func (t *CopyTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *CopyTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}
