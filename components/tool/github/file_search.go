package github

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const fileSearchDescription = `
** General Purpose **
It searches file contents in a previously cloned GitHub repository using a regex
(equivalent to 'grep -rn pattern dir/').

** Output **
It returns {results:[{path, line, content}], hasMore, nextOffset}. Results are
ordered by file path (lexical) and then by line number.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- Binary files and the .git directory are skipped.
- MaxResults (default 100) caps the number of returned matches.
- Use Offset with NextOffset to page through large result sets.
- Pattern uses Go RE2 syntax; lookahead/lookbehind/backreferences are NOT supported.
`

// FileSearchParams defines the parameters for searching file contents.
type FileSearchParams struct {
	Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	Pattern    string `json:"pattern"    validate:"required" jsonschema:"(required) Go RE2 regex to search for in file contents."`
	PathPrefix string `json:"pathPrefix,omitempty" jsonschema:"(optional) Subdirectory to search within. Defaults to repo root."`
	Offset     int    `json:"offset,omitempty" validate:"omitempty,gte=0" jsonschema:"(optional) Number of matches to skip. Defaults to 0."`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"(optional) Maximum number of matches to return. Defaults to 100."`
}

// FileSearchOutput describes one matching line.
type FileSearchOutput struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// FileSearchResult is a page of content-search matches.
type FileSearchResult struct {
	Results    []FileSearchOutput `json:"results"`
	HasMore    bool               `json:"hasMore"`
	NextOffset int                `json:"nextOffset"`
}

// FileSearchTool is an eino tool for searching file contents.
type FileSearchTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke searches file contents with the requested regex.
func (t *FileSearchTool) Invoke(ctx context.Context, params *FileSearchParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return "", errors.Wrapf(err, "invalid search pattern %q", params.Pattern)
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

	if err := fileutil.RejectDotGitPath(params.PathPrefix); err != nil {
		return "", err
	}

	searchRoot, err := fileutil.ValidateRelativePath(clonePath_, params.PathPrefix)
	if err != nil {
		return "", err
	}

	// Reject if the search root itself is a symlink.
	if fi, statErr := os.Lstat(searchRoot); statErr == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.Errorf("path %q is a symlink; symlinks are not allowed", params.PathPrefix)
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

	results := make([]FileSearchOutput, 0, maxResults)
	hasMore := false
	seen := 0
	err = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check symlink via Lstat; skip on error to avoid stopping the walk.
		fi, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Skip files that are too large to prevent memory exhaustion.
		if fi.Size() > fileutil.DefaultMaxSearchFileBytes {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if fileutil.IsBinary(data) {
			return nil
		}

		relPath, _ := filepath.Rel(clonePath_, path)
		relPath = filepath.ToSlash(relPath)

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			if seen < offset {
				seen++
				continue
			}
			if len(results) >= maxResults {
				hasMore = true
				return filepath.SkipAll
			}
			results = append(results, FileSearchOutput{
				Path:    relPath,
				Line:    i + 1,
				Content: line,
			})
			seen++
		}
		return nil
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to search files")
	}

	output := &FileSearchResult{
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

// NewFileSearchTool creates a new FileSearchTool.
func NewFileSearchTool(ctx context.Context, configs Configs) (*FileSearchTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newFileSearchTool(ctx, base)
}

func newFileSearchTool(ctx context.Context, base *baseTool) (*FileSearchTool, error) {
	searchTool := &FileSearchTool{baseTool: base}
	t, err := utils.InferTool("github_file_search", fileSearchDescription, searchTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	searchTool.InvokableTool = t
	return searchTool, nil
}
