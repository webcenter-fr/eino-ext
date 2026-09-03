package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const fileMoveDescription = `
** General Purpose **
It moves or renames a file or directory from a source path to a destination path
within the session-scoped temporary directory.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
and move status.

** Important **
- Both regular files and directories can be moved/renamed.
- If the destination exists, it is overwritten.
- Destination parent directories are created if they don't exist.
- If os.Rename fails (e.g., cross-device), falls back to copy+delete, which is
  not atomic: if removal of the source fails after copying, both copies exist.
- Symlinks are always rejected.
- Symlinks inside a moved directory tree are skipped.
- The destination must not be inside the source directory.
- Moving a directory onto an existing non-empty directory is not supported.
- Path traversal attempts are rejected.
- Source and destination must differ.
`

// MoveParams holds the parameters for the file_move tool.
type MoveParams struct {
	Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path."`
	Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path."`
}

// MoveOutput is the JSON result returned by the file_move tool.
type MoveOutput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"` // "file" or "dir"
	Moved       bool   `json:"moved"`
}

// MoveTool is the eino tool for the file_move operation within the
// session-scoped directory. It implements tool.InvokableTool.
type MoveTool struct {
	cfg       *Config
	invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*MoveTool)(nil)

// Invoke validates the parameters and performs the move or rename operation.
func (t *MoveTool) Invoke(ctx context.Context, params *MoveParams) (string, error) {
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
	isDir := srcFi.IsDir()

	// Reject type mismatches if destination exists.
	if dstFi, statErr := os.Lstat(dstSafePath); statErr == nil {
		if dstFi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("destination %q is a symlink; symlinks are not allowed", params.Destination)
		}
		if dstFi.IsDir() && !isDir {
			return "", errors.Errorf("destination %q is a directory but source is a file", params.Destination)
		}
		if !dstFi.IsDir() && isDir {
			return "", errors.Errorf("destination %q is a file but source is a directory", params.Destination)
		}
	}

	moveErr := os.Rename(srcSafePath, dstSafePath)
	if moveErr != nil {
		linkErr, ok := moveErr.(*os.LinkError)
		if !ok || linkErr.Err != syscall.EXDEV {
			return "", errors.Wrapf(moveErr, "failed to move %q to %q", params.Source, params.Destination)
		}
		// Cross-device fallback: copy + delete.
		if isDir {
			if _, _, copyErr := fileutil.CopyDir(srcSafePath, dstSafePath, false); copyErr != nil {
				return "", errors.Wrapf(copyErr, "failed to copy directory %q to %q for cross-device move", params.Source, params.Destination)
			}
			if rmErr := os.RemoveAll(srcSafePath); rmErr != nil {
				return "", errors.Wrapf(rmErr, "copied directory %q to %q but failed to remove the source; both copies now exist", params.Source, params.Destination)
			}
		} else {
			if copyErr := fileutil.CopyFileContents(srcSafePath, dstSafePath); copyErr != nil {
				// Best-effort cleanup of the partial destination; the copy
				// error is what matters, so cleanup failures are ignored.
				_ = os.Remove(dstSafePath)
				return "", errors.Wrapf(copyErr, "failed to copy file %q to %q for cross-device move", params.Source, params.Destination)
			}
			if rmErr := os.Remove(srcSafePath); rmErr != nil {
				return "", errors.Wrapf(rmErr, "copied file %q to %q but failed to remove the source; both copies now exist", params.Source, params.Destination)
			}
		}
	}

	outputType := "file"
	if isDir {
		outputType = "dir"
	}

	output := &MoveOutput{
		Source:      params.Source,
		Destination: params.Destination,
		Type:        outputType,
		Moved:       true,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewMoveTool creates the file_move tool against the given configuration.
func NewMoveTool(ctx context.Context, cfg *Config) (*MoveTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}
	moveTool := &MoveTool{cfg: cfg}
	t, err := utils.InferTool("file_move", fileMoveDescription, moveTool.Invoke)
	if err != nil {
		return nil, err
	}
	moveTool.invokable = t
	return moveTool, nil
}

// Info implements tool.InvokableTool.
func (t *MoveTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *MoveTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}
