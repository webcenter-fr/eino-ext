package github

import (
	"context"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileWriteDescription = `
** General Purpose **
It creates or overwrites a file in a previously cloned GitHub repository on the
local filesystem. This tool does NOT commit or push — use github_commit_push to
persist changes.

** Output **
It returns the file path and the number of bytes written.

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- Paths are validated to stay within the clone directory.
- The .git directory and symlinks are always rejected.
- Executes directly without confirmation. Set DryRun=true to preview without making changes.
`

// FileWriteParams defines the parameters for writing a file in a clone.
type FileWriteParams struct {
	Instance string `json:"instance"       validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner"          validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo"           validate:"required" jsonschema:"(required) Repository name."`
	Path     string `json:"path"           validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
	Content  string `json:"content"        validate:"required" jsonschema:"(required) File content to write."`
	DryRun   bool   `json:"dryRun,omitempty"         jsonschema:"(optional) If true, preview the write without making changes."`
}

// FileWriteOutput is the result of a local file write.
type FileWriteOutput struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

// FileWriteTool is an eino tool for writing files in a clone.
type FileWriteTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke writes the file to the local clone without committing.
func (t *FileWriteTool) Invoke(ctx context.Context, params *FileWriteParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if len(params.Content) > fileutil.DefaultMaxWriteBytes {
		return "", errors.Errorf("parameter 'content' size %d bytes exceeds the maximum %d bytes; reduce the content and retry", len(params.Content), fileutil.DefaultMaxWriteBytes)
	}

	if params.DryRun {
		preview := map[string]any{
			"dryRun": true,
			"wouldWrite": map[string]any{
				"path":  params.Path,
				"bytes": len(params.Content),
			},
		}
		previewJSON, err := json.Marshal(preview)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(previewJSON), nil
	}

	clonePath_, err := t.clonePathForSession(ctx, params.Owner, params.Repo)
	if err != nil {
		return "", err
	}
	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	if err := t.touchCloneSession(ctx); err != nil {
		return "", err
	}

	if err := fileutil.RejectDotGitPath(params.Path); err != nil {
		return "", err
	}

	fullPath, err := fileutil.ValidateRelativePath(clonePath_, params.Path)
	if err != nil {
		return "", err
	}

	// Resolve symlinks at every path component to prevent symlink-based
	// directory traversal. A malicious repo can contain symlinks that would
	// redirect file writes outside the clone directory.
	safePath, err := fileutil.ResolveSymlinkSafe(clonePath_, fullPath, true)
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

	// Parent directories were already created by fileutil.ResolveSymlinkSafe (createDirs=true).
	if err := os.WriteFile(safePath, []byte(params.Content), 0o644); err != nil {
		return "", errors.Wrapf(err, "failed to write file %q", params.Path)
	}

	output := &FileWriteOutput{
		Path:  params.Path,
		Bytes: len(params.Content),
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewFileWriteTool creates a new FileWriteTool.
func NewFileWriteTool(ctx context.Context, configs Configs) (*FileWriteTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileWriteTool(ctx, base)
}

func newFileWriteTool(ctx context.Context, base *baseTool) (*FileWriteTool, error) {
	writeTool := &FileWriteTool{baseTool: base}
	t, err := utils.InferTool("github_file_write", fileWriteDescription, writeTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	writeTool.InvokableTool = t
	return writeTool, nil
}
