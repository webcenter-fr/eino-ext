package github

import (
	"context"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileDeleteDescription = `
** General Purpose **
It deletes a file or directory from a previously cloned GitHub repository on the
local filesystem. This tool does NOT commit or push — use github_file_write to
persist changes.

** Output **
It returns a JSON object with the deleted path, type ("file" or "dir"), deletion
status, and branch.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be deleted. Directory deletion is recursive.
- The clone root itself (".") cannot be deleted.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Path traversal attempts are rejected.
- Requires Confirmed=true. Use DryRun=true first to preview.
`

type FileDeleteParams struct {
	Instance  string `json:"instance"  validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner"     validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo"      validate:"required" jsonschema:"(required) Repository name."`
	Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file or directory path inside the cloned repo to delete."`
	Branch    string `json:"branch"    validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
	DryRun    bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the deletion without making changes."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

type FileDeleteOutput struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // "file" or "dir"
	Deleted bool   `json:"deleted"`
	Branch  string `json:"branch"`
}

type FileDeleteTool struct {
	*baseTool
	tool.InvokableTool
}

var _ tool.InvokableTool = (*FileDeleteTool)(nil)

func (t *FileDeleteTool) Invoke(ctx context.Context, params *FileDeleteParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		clonePath_ := t.clonePathForSession(ctx, params.Owner, params.Repo)
		if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
			return "", err
		}
		if err := fileutil.RejectDotGitPath(params.Path); err != nil {
			return "", err
		}
		if err := rejectCloneRootPath(params.Path); err != nil {
			return "", err
		}
		fullPath, err := fileutil.ValidateRelativePath(clonePath_, params.Path)
		if err != nil {
			return "", err
		}
		safePath, err := fileutil.ResolveSymlinkSafe(clonePath_, fullPath, false)
		if err != nil {
			return "", err
		}
		fi, err := os.Lstat(safePath)
		if err != nil {
			return "", errors.Wrapf(err, "path %q not found in clone", params.Path)
		}
		wouldDelete := map[string]any{
			"path":   params.Path,
			"type":   "file",
			"branch": params.Branch,
		}
		if fi.IsDir() {
			wouldDelete["type"] = "dir"
			files, err := fileutil.WalkDirFiles(safePath, true)
			if err != nil {
				return "", errors.Wrapf(err, "failed to walk directory %q", params.Path)
			}
			wouldDelete["files"] = prefixedRelPaths(files, params.Path)
		}
		preview, err := json.Marshal(map[string]any{
			"dryRun":      true,
			"wouldDelete": wouldDelete,
		})
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(preview), nil
	}

	if err := confirm.RequireConfirmationForAction("delete file", params.Confirmed); err != nil {
		return "", err
	}

	clonePath_ := t.clonePathForSession(ctx, params.Owner, params.Repo)
	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	if err := fileutil.RejectDotGitPath(params.Path); err != nil {
		return "", err
	}

	if err := rejectCloneRootPath(params.Path); err != nil {
		return "", err
	}

	fullPath, err := fileutil.ValidateRelativePath(clonePath_, params.Path)
	if err != nil {
		return "", err
	}

	// Resolve symlinks at every path component to prevent symlink-based
	// directory traversal. createDirs=false: deleting never creates paths.
	safePath, err := fileutil.ResolveSymlinkSafe(clonePath_, fullPath, false)
	if err != nil {
		return "", err
	}

	fi, err := os.Lstat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "path %q not found in clone", params.Path)
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

	output := &FileDeleteOutput{
		Path:    params.Path,
		Type:    deletedType,
		Deleted: true,
		Branch:  params.Branch,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

func NewFileDeleteTool(ctx context.Context, configs Configs) (*FileDeleteTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileDeleteTool(ctx, base)
}

func newFileDeleteTool(ctx context.Context, base *baseTool) (*FileDeleteTool, error) {
	deleteTool := &FileDeleteTool{baseTool: base}
	t, err := utils.InferTool("github_file_delete", fileDeleteDescription, deleteTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	deleteTool.InvokableTool = t
	return deleteTool, nil
}
