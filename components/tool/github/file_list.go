package github

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileListDescription = `
** General Purpose **
It lists files and directories in a previously cloned GitHub repository.

** Output **
It returns {results:[{name, type, size, path}], hasMore, nextOffset}. Results are
ordered by path (lexical).

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- MaxDepth=1 (default) lists immediate children; 0 = unlimited depth (capped by MaxResults).
- Use Offset with NextOffset to page through large result sets.
- Symlinks are skipped. The .git directory is always skipped.
`

// FileListParams defines the parameters for listing files in a clone.
type FileListParams struct {
	Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	SubPath    string `json:"subPath,omitempty" jsonschema:"(optional) Relative subdirectory to list. Defaults to repo root."`
	MaxDepth   *int   `json:"maxDepth,omitempty" validate:"omitempty,gte=0" jsonschema:"(optional) Maximum recursion depth. 1 = immediate children (default). 0 = unlimited."`
	Offset     int    `json:"offset,omitempty" validate:"omitempty,gte=0" jsonschema:"(optional) Number of entries to skip. Defaults to 0."`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"(optional) Maximum number of entries to return. Defaults to 100."`
}

// FileListOutput describes one file or directory entry.
type FileListOutput struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "dir"
	Size int64  `json:"size"`
	Path string `json:"path"` // relative to repo root
}

// FileListResult is a page of file-list entries.
type FileListResult struct {
	Results    []FileListOutput `json:"results"`
	HasMore    bool             `json:"hasMore"`
	NextOffset int              `json:"nextOffset"`
}

// FileListTool is an eino tool for listing files in a clone.
type FileListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke lists files and directories under the requested sub-path.
func (t *FileListTool) Invoke(ctx context.Context, params *FileListParams) (string, error) {
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

	if err := t.touchCloneSession(ctx); err != nil {
		return "", err
	}

	if err := fileutil.RejectDotGitPath(params.SubPath); err != nil {
		return "", err
	}

	listRoot, err := fileutil.ValidateRelativePath(clonePath_, params.SubPath)
	if err != nil {
		return "", err
	}

	// Reject if the list root itself is a symlink.
	if fi, statErr := os.Lstat(listRoot); statErr == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("path %q is a symlink; symlinks are not allowed", params.SubPath)
		}
	}

	maxDepth := 1
	unlimitedDepth := false
	if params.MaxDepth != nil {
		if *params.MaxDepth == 0 {
			unlimitedDepth = true
		} else {
			maxDepth = *params.MaxDepth
		}
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	results := make([]FileListOutput, 0, maxResults)
	hasMore := false
	seen := 0
	err = filepath.WalkDir(listRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip root entry itself
		if path == listRoot {
			return nil
		}

		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		// Check symlink via Lstat; skip on error to avoid stopping the walk.
		fi, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		relPath, _ := filepath.Rel(listRoot, path)
		relPath = filepath.ToSlash(relPath)
		fullRelPath, _ := filepath.Rel(clonePath_, path)
		fullRelPath = filepath.ToSlash(fullRelPath)

		depth := strings.Count(relPath, "/") + 1
		withinDepth := unlimitedDepth || depth <= maxDepth

		if withinDepth {
			if seen < offset {
				seen++
			} else if len(results) >= maxResults {
				hasMore = true
				return filepath.SkipAll
			} else {
				entryType := "file"
				if d.IsDir() {
					entryType = "dir"
				}
				results = append(results, FileListOutput{
					Name: d.Name(),
					Type: entryType,
					Size: fi.Size(),
					Path: fullRelPath,
				})
				seen++
			}
		}

		// Don't descend into directories at or beyond maxDepth.
		if !unlimitedDepth && d.IsDir() && depth >= maxDepth {
			return filepath.SkipDir
		}

		return nil
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to list files")
	}

	output := &FileListResult{
		Results:    results,
		HasMore:    hasMore,
		NextOffset: offset + len(results),
	}
	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(data), nil
}

// NewFileListTool creates a new FileListTool.
func NewFileListTool(ctx context.Context, configs Configs) (*FileListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileListTool(ctx, base)
}

func newFileListTool(ctx context.Context, base *baseTool) (*FileListTool, error) {
	listTool := &FileListTool{baseTool: base}
	t, err := utils.InferTool("github_file_list", fileListDescription, listTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t
	return listTool, nil
}
