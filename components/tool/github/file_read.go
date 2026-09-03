package github

import (
	"context"
	"fmt"
	"os"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileReadDescription = `
** General Purpose **
It reads the contents of a file from a previously cloned GitHub repository.

** Output **
It returns a JSON object with the file path, content (or a line range), byte size,
and a truncated flag if the file exceeds the read limit.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Paths are validated to stay within the clone directory; traversal attempts are rejected.
- Binary files are detected and refused.
- Files larger than 1MB are truncated with a note.
`

type FileReadParams struct {
	Instance  string `json:"instance"  validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner"     validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo"      validate:"required" jsonschema:"(required) Repository name."`
	Path      string `json:"path"      validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
	StartLine int    `json:"startLine,omitempty" jsonschema:"(optional) 1-indexed first line to read. 0 = start from beginning."`
	EndLine   int    `json:"endLine,omitempty"   jsonschema:"(optional) 1-indexed last line to read. 0 = read to end."`
}

type FileReadOutput struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

type FileReadTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *FileReadTool) Invoke(ctx context.Context, params *FileReadParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	// Validate StartLine/EndLine
	if params.StartLine > 0 && params.EndLine > 0 && params.StartLine > params.EndLine {
		return "", errors.Errorf("parameter 'startLine' (%d) must be <= 'endLine' (%d); swap or correct the values and retry", params.StartLine, params.EndLine)
	}

	clonePath_ := t.clonePathForSession(ctx, params.Owner, params.Repo)
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

	// Resolve symlinks at every path component to prevent symlink-based
	// directory traversal. A malicious repo can contain symlinks (e.g.,
	// "link -> /etc") that os.ReadFile would follow, reading files outside
	// the clone. ValidateRelativePath only does a lexical check; this walk
	// rejects symlinks at any level.
	safePath, err := fileutil.ResolveSymlinkSafe(clonePath_, fullPath, false)
	if err != nil {
		return "", err
	}

	// Check if the resolved path is a directory (clearer error than ReadFile's).
	if fi, statErr := os.Lstat(safePath); statErr == nil {
		if fi.IsDir() {
			return "", errors.Errorf("path %q is a directory, not a file", params.Path)
		}
	}

	data, err := os.ReadFile(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Wrapf(err, "file %q not found in clone", params.Path)
		}
		return "", errors.Wrapf(err, "failed to read file %q", params.Path)
	}

	if fileutil.IsBinary(data) {
		return "", errors.Errorf("file %q appears to be binary; refusing to read", params.Path)
	}

	output := &FileReadOutput{
		Path:  params.Path,
		Bytes: len(data),
	}

	if len(data) > fileutil.DefaultMaxReadBytes {
		output.Truncated = true
		output.Note = fmt.Sprintf("file truncated to 1MB (original size: %d bytes)", len(data))
		data = data[:fileutil.DefaultMaxReadBytes]
		output.Bytes = len(data)
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

func NewFileReadTool(ctx context.Context, configs Configs) (*FileReadTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileReadTool(ctx, base)
}

func newFileReadTool(ctx context.Context, base *baseTool) (*FileReadTool, error) {
	readTool := &FileReadTool{baseTool: base}
	t, err := utils.InferTool("github_file_read", fileReadDescription, readTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	readTool.InvokableTool = t
	return readTool, nil
}
