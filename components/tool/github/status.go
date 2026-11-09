package github

import (
	"context"
	"sort"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/goccy/go-json"
)

const statusDescription = `
** General Purpose **
It reports the working-tree status of a previously cloned GitHub repository:
the current branch, whether the worktree is clean, and the changed files.

** Output **
It returns {branch, clean, files:[{path, status}], truncated}.

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- Status includes both staged and unstaged changes.
- Files are sorted by path for stable output.
- MaxResults (default 1000) caps the number of returned files.
`

// StatusParams defines the parameters for reading worktree status.
type StatusParams struct {
	Instance   string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner      string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo       string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	MaxResults int    `json:"maxResults,omitempty" validate:"omitempty,gte=0" jsonschema:"(optional) Maximum number of changed files to return. Defaults to 1000."`
}

// FileStatusOutput describes one changed file.
type FileStatusOutput struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// StatusOutput is the worktree status summary.
type StatusOutput struct {
	Branch    string             `json:"branch"`
	Clean     bool               `json:"clean"`
	Files     []FileStatusOutput `json:"files"`
	Truncated bool               `json:"truncated"`
}

// StatusTool is an eino tool for reading worktree status.
type StatusTool struct {
	*baseTool
	tool.InvokableTool
}

const defaultStatusMaxFiles = 1000

// Invoke returns the current branch, clean state, and changed files.
func (t *StatusTool) Invoke(ctx context.Context, params *StatusParams) (string, error) {
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

	repo, err := git.PlainOpen(clonePath_)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open cloned repository at %q; run github_repo_clone first", clonePath_)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}
	status, err := wt.Status()
	if err != nil {
		return "", errors.Wrap(err, "failed to read worktree status")
	}

	branch := ""
	if head, headErr := repo.Head(); headErr == nil {
		if head.Name().IsBranch() {
			branch = head.Name().Short()
		} else {
			branch = "detached"
		}
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = defaultStatusMaxFiles
	}

	paths := make([]string, 0, len(status))
	for path := range status {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]FileStatusOutput, 0, min(len(paths), maxResults))
	for _, path := range paths {
		if len(files) >= maxResults {
			break
		}
		files = append(files, FileStatusOutput{
			Path:   path,
			Status: fileStatusString(status[path]),
		})
	}

	output := &StatusOutput{
		Branch:    branch,
		Clean:     status.IsClean(),
		Files:     files,
		Truncated: len(paths) > len(files),
	}
	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

// NewStatusTool creates a new StatusTool.
func NewStatusTool(ctx context.Context, configs Configs) (*StatusTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newStatusTool(ctx, base)
}

func newStatusTool(ctx context.Context, base *baseTool) (*StatusTool, error) {
	statusTool := &StatusTool{baseTool: base}
	t, err := utils.InferTool("github_status", statusDescription, statusTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	statusTool.InvokableTool = t
	return statusTool, nil
}
