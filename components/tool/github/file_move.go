package github

import (
	"context"
	"os"
	"syscall"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileMoveDescription = `
** General Purpose **
It moves or renames a file or directory from a source path to a destination path
within a previously cloned GitHub repository on the local filesystem. This tool
does NOT commit or push — use github_file_write to persist changes.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
move status, and branch.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be moved/renamed.
- If the destination exists, it is overwritten.
- Destination parent directories are created if they don't exist.
- If os.Rename fails (e.g., cross-device), falls back to copy+delete, which is
  not atomic: if removal of the source fails after copying, both copies exist.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Symlinks and .git directories inside a moved directory tree are skipped.
- The destination must not be inside the source directory.
- Moving a directory onto an existing non-empty directory is not supported.
- Path traversal attempts are rejected.
- Source and destination must differ.
- Requires Confirmed=true. Use DryRun=true first to preview.
`

type FileMoveParams struct {
	Instance    string `json:"instance"    validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner       string `json:"owner"       validate:"required" jsonschema:"(required) Repository owner."`
	Repo        string `json:"repo"        validate:"required" jsonschema:"(required) Repository name."`
	Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path inside the cloned repo."`
	Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path inside the cloned repo."`
	Branch      string `json:"branch"      validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
	DryRun      bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the move without making changes."`
	Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

type FileMoveOutput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"` // "file" or "dir"
	Moved       bool   `json:"moved"`
	Branch      string `json:"branch"`
}

type FileMoveTool struct {
	*baseTool
	tool.InvokableTool
}

var _ tool.InvokableTool = (*FileMoveTool)(nil)

func (t *FileMoveTool) Invoke(ctx context.Context, params *FileMoveParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.Source == params.Destination {
		return "", errors.Errorf("source and destination are the same path %q", params.Source)
	}

	clonePath_ := t.clonePathForSession(ctx, params.Owner, params.Repo)

	if params.DryRun {
		if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
			return "", err
		}
		return transferDryRunPreview(clonePath_, params.Source, params.Destination, params.Branch, "wouldMove")
	}

	if err := confirm.RequireConfirmationForActionCtx(ctx, "github_file_move", "move file", params.Confirmed); err != nil {
		return "", err
	}

	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	srcSafePath, dstSafePath, isDir, err := validateTransferPaths(clonePath_, params.Source, params.Destination)
	if err != nil {
		return "", err
	}

	// Attempt an atomic rename first.
	moveErr := os.Rename(srcSafePath, dstSafePath)
	if moveErr != nil {
		linkErr, ok := moveErr.(*os.LinkError)
		if !ok || linkErr.Err != syscall.EXDEV {
			return "", errors.Wrapf(moveErr, "failed to move %q to %q", params.Source, params.Destination)
		}
		// os.Rename fails across mount points (EXDEV); fall back to copy+delete.
		if isDir {
			if _, _, copyErr := fileutil.CopyDir(srcSafePath, dstSafePath, true); copyErr != nil {
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

	output := &FileMoveOutput{
		Source:      params.Source,
		Destination: params.Destination,
		Type:        outputType,
		Moved:       true,
		Branch:      params.Branch,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

func NewFileMoveTool(ctx context.Context, configs Configs) (*FileMoveTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileMoveTool(ctx, base)
}

func newFileMoveTool(ctx context.Context, base *baseTool) (*FileMoveTool, error) {
	moveTool := &FileMoveTool{baseTool: base}
	t, err := utils.InferTool("github_file_move", fileMoveDescription, moveTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	moveTool.InvokableTool = t
	return moveTool, nil
}
