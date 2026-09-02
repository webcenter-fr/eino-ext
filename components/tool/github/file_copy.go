package github

import (
	"context"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const fileCopyDescription = `
** General Purpose **
It copies a file or directory from a source path to a destination path within a
previously cloned GitHub repository on the local filesystem. This tool does NOT
commit or push — use github_file_write to persist changes.

** Output **
It returns a JSON object with the source, destination, type ("file" or "dir"),
copy status, branch, and (for directories) file count and total bytes.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Both regular files and directories can be copied.
- If the destination exists, it is overwritten (files: truncated; dirs: merged).
- Destination parent directories are created if they don't exist.
- The .git directory is always rejected.
- Symlinks are always rejected.
- Symlinks and .git directories inside a copied directory tree are skipped.
- The destination must not be inside the source directory.
- Path traversal attempts are rejected.
- Source and destination must differ.
- Requires Confirmed=true. Use DryRun=true first to preview.
`

type FileCopyParams struct {
	Instance    string `json:"instance"    validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner       string `json:"owner"       validate:"required" jsonschema:"(required) Repository owner."`
	Repo        string `json:"repo"        validate:"required" jsonschema:"(required) Repository name."`
	Source      string `json:"source"      validate:"required" jsonschema:"(required) Relative source file or directory path inside the cloned repo."`
	Destination string `json:"destination" validate:"required" jsonschema:"(required) Relative destination file or directory path inside the cloned repo."`
	Branch      string `json:"branch"      validate:"required" jsonschema:"(required) Target branch (for context; no checkout is performed)."`
	DryRun      bool   `json:"dryRun,omitempty"    jsonschema:"(optional) If true, preview the copy without making changes."`
	Confirmed   bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

type FileCopyOutput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"` // "file" or "dir"
	Copied      bool   `json:"copied"`
	Branch      string `json:"branch"`
	FileCount   int    `json:"fileCount,omitempty"`  // only for directory copies
	TotalBytes  int64  `json:"totalBytes,omitempty"` // only for directory copies
}

type FileCopyTool struct {
	*baseTool
	tool.InvokableTool
}

var _ tool.InvokableTool = (*FileCopyTool)(nil)

func (t *FileCopyTool) Invoke(ctx context.Context, params *FileCopyParams) (string, error) {
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
		return transferDryRunPreview(clonePath_, params.Source, params.Destination, params.Branch, "wouldCopy")
	}

	if err := confirm.RequireConfirmationForAction("copy file", params.Confirmed); err != nil {
		return "", err
	}

	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	srcSafePath, dstSafePath, isDir, err := validateTransferPaths(clonePath_, params.Source, params.Destination)
	if err != nil {
		return "", err
	}

	output := &FileCopyOutput{
		Source:      params.Source,
		Destination: params.Destination,
		Type:        "file",
		Copied:      true,
		Branch:      params.Branch,
	}

	if isDir {
		output.Type = "dir"
		fileCount, totalBytes, err := copyDir(srcSafePath, dstSafePath)
		if err != nil {
			return "", errors.Wrapf(err, "failed to copy directory %q to %q", params.Source, params.Destination)
		}
		output.FileCount = fileCount
		output.TotalBytes = totalBytes
	} else {
		if err := copyFileContents(srcSafePath, dstSafePath); err != nil {
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

func NewFileCopyTool(ctx context.Context, configs Configs) (*FileCopyTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileCopyTool(ctx, base)
}

func newFileCopyTool(ctx context.Context, base *baseTool) (*FileCopyTool, error) {
	copyTool := &FileCopyTool{baseTool: base}
	t, err := utils.InferTool("github_file_copy", fileCopyDescription, copyTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	copyTool.InvokableTool = t
	return copyTool, nil
}
