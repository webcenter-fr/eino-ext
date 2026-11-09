package github

import (
	"context"
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileEditDescription = `
** General Purpose **
It replaces an exact string in a file of a previously cloned GitHub repository on
the local filesystem. This tool does NOT commit or push — use github_commit_push
to persist changes.

** Output **
It returns the file path, the number of replacements, and the resulting file size.

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- oldString must occur exactly once unless replaceAll=true.
- The .git directory and symlinks are always rejected.
- Executes directly without confirmation. Set DryRun=true to preview the replacement count.
`

// FileEditParams defines the parameters for an exact-string file edit.
type FileEditParams struct {
	Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	Path       string `json:"path"       validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
	OldString  string `json:"oldString"  validate:"required" jsonschema:"(required) Exact string to replace."`
	NewString  string `json:"newString"  jsonschema:"(optional) Replacement string. Empty deletes oldString."`
	ReplaceAll bool   `json:"replaceAll,omitempty" jsonschema:"(optional) Replace every occurrence instead of requiring a unique match."`
	DryRun     bool   `json:"dryRun,omitempty"     jsonschema:"(optional) If true, preview the replacement count without writing."`
}

// FileEditOutput is the result of a file edit.
type FileEditOutput struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	Bytes        int    `json:"bytes"`
}

// FileEditTool is an eino tool for exact-string file edits.
type FileEditTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke replaces oldString with newString in the target file.
func (t *FileEditTool) Invoke(ctx context.Context, params *FileEditParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	clonePath_, err := t.clonePathForSession(ctx, params.Owner, params.Repo)
	if err != nil {
		return "", err
	}
	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	if err := fileutil.RejectDotGitPath(params.Path); err != nil {
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
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "path %q not found in clone", params.Path)
		}
		return "", errors.Wrapf(err, "failed to stat path %q", params.Path)
	}
	if fi.IsDir() {
		return "", errors.Errorf("path %q is a directory; cannot edit a directory", params.Path)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.Errorf("path %q is a symlink; symlinks are not allowed", params.Path)
	}

	content, err := os.ReadFile(safePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read file %q", params.Path)
	}

	count := strings.Count(string(content), params.OldString)
	if count == 0 {
		return "", errors.Errorf("oldString was not found in %q", params.Path)
	}
	if count > 1 && !params.ReplaceAll {
		return "", errors.Errorf("oldString occurs %d times in %q; set replaceAll=true to replace every occurrence", count, params.Path)
	}

	replacements := 1
	replaceCount := 1
	if params.ReplaceAll {
		replacements = count
		replaceCount = -1
	}
	newContent := strings.Replace(string(content), params.OldString, params.NewString, replaceCount)

	if params.DryRun {
		preview := map[string]any{
			"dryRun": true,
			"wouldEdit": map[string]any{
				"path":         params.Path,
				"replacements": replacements,
				"bytes":        len(newContent),
			},
		}
		previewJSON, err := json.Marshal(preview)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(previewJSON), nil
	}

	if err := t.touchCloneSession(ctx); err != nil {
		return "", err
	}

	if err := os.WriteFile(safePath, []byte(newContent), 0o644); err != nil {
		return "", errors.Wrapf(err, "failed to write file %q", params.Path)
	}

	output := &FileEditOutput{
		Path:         params.Path,
		Replacements: replacements,
		Bytes:        len(newContent),
	}
	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewFileEditTool creates a new FileEditTool.
func NewFileEditTool(ctx context.Context, configs Configs) (*FileEditTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileEditTool(ctx, base)
}

func newFileEditTool(ctx context.Context, base *baseTool) (*FileEditTool, error) {
	editTool := &FileEditTool{baseTool: base}
	t, err := utils.InferTool("github_file_edit", fileEditDescription, editTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	editTool.InvokableTool = t
	return editTool, nil
}
