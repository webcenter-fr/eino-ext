package file

import (
	"context"
	"os"
	"path/filepath"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const fileDeleteDescription = `
** General Purpose **
It deletes a file or directory from the session-scoped temporary directory.

** Output **
It returns a JSON object with the deleted path, type ("file" or "dir"), and
deletion status.

** Important **
- Paths are relative to the session directory and validated to prevent traversal.
- Both regular files and directories can be deleted. Directory deletion is recursive.
- The session root itself (".") cannot be deleted.
- Symlinks are always rejected.
`

// DeleteParams holds the parameters for the file_delete tool.
type DeleteParams struct {
	Path string `json:"path" validate:"required" jsonschema:"(required) Relative file or directory path inside the session directory to delete."`
}

// DeleteOutput is the JSON result returned by the file_delete tool.
type DeleteOutput struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // "file" or "dir"
	Deleted bool   `json:"deleted"`
}

// DeleteTool is the eino tool for the file_delete operation within the
// session-scoped directory. It implements tool.InvokableTool.
type DeleteTool struct {
	cfg       *Config
	invokable tool.InvokableTool
}

var _ tool.InvokableTool = (*DeleteTool)(nil)

// Invoke validates the parameters and performs the delete operation.
func (t *DeleteTool) Invoke(ctx context.Context, params *DeleteParams) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	// Reject deleting the session root.
	if filepath.Clean(filepath.FromSlash(params.Path)) == "." {
		return "", errors.Errorf("path %q refers to the session root; deleting the entire session directory is not allowed", params.Path)
	}

	safePath, err := resolvePath(t.cfg.Workdir, ctx, params.Path, false)
	if err != nil {
		return "", err
	}

	fi, err := os.Lstat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "path %q not found", params.Path)
		}
		return "", errors.Wrapf(err, "failed to stat path %q", params.Path)
	}

	deletedType := "file"
	var deleteErr error
	if fi.IsDir() {
		deletedType = "dir"
		deleteErr = os.RemoveAll(safePath)
	} else {
		deleteErr = os.Remove(safePath)
	}
	if deleteErr != nil {
		return "", errors.Wrapf(deleteErr, "failed to delete %q", params.Path)
	}

	output := &DeleteOutput{
		Path:    params.Path,
		Type:    deletedType,
		Deleted: true,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewDeleteTool creates the file_delete tool against the given configuration.
func NewDeleteTool(ctx context.Context, cfg *Config) (*DeleteTool, error) {
	cfg, err := newConfig(cfg)
	if err != nil {
		return nil, err
	}
	deleteTool := &DeleteTool{cfg: cfg}
	t, err := utils.InferTool("file_delete", fileDeleteDescription, deleteTool.Invoke)
	if err != nil {
		return nil, err
	}
	deleteTool.invokable = t
	return deleteTool, nil
}

// Info implements tool.InvokableTool.
func (t *DeleteTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

// InvokableRun implements tool.InvokableTool.
func (t *DeleteTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return t.invokable.InvokableRun(ctx, args, opts...)
}
