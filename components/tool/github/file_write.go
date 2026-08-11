package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const fileWriteDescription = `
** General Purpose **
It creates or overwrites a file in a previously cloned GitHub repository, commits
the change, and pushes to the specified remote branch.

** Output **
It returns the file path, commit SHA, and push status.

** Important **
- The repository must already be cloned under <CloneDir>/<owner>/<repo>.
- If the target branch does not exist locally, it is created from BaseBranch
  (defaults to current HEAD).
- Requires Confirmed=true. Use DryRun=true first to preview.
- Paths are validated to stay within the clone directory.
- Never force-pushes; a non-fast-forward rejection returns an error.
`

type FileWriteParams struct {
	Instance      string `json:"instance"       validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner         string `json:"owner"          validate:"required" jsonschema:"(required) Repository owner."`
	Repo          string `json:"repo"           validate:"required" jsonschema:"(required) Repository name."`
	Path          string `json:"path"           validate:"required" jsonschema:"(required) Relative file path inside the cloned repo."`
	Content       string `json:"content"        validate:"required" jsonschema:"(required) File content to write."`
	Branch        string `json:"branch"         validate:"required" jsonschema:"(required) Target branch to commit and push to."`
	BaseBranch    string `json:"baseBranch,omitempty" jsonschema:"(optional) Base branch to create Branch from if Branch does not exist locally. Defaults to current HEAD."`
	CommitMessage string `json:"commitMessage"  validate:"required" jsonschema:"(required) Git commit message."`
	DryRun        bool   `json:"dryRun,omitempty"         jsonschema:"(optional) If true, preview the write without making changes."`
	Confirmed     bool   `json:"confirmed,omitempty"       jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

type FileWriteOutput struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	CommitSHA string `json:"commitSha"`
	Pushed    bool   `json:"pushed"`
}

type FileWriteTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *FileWriteTool) Invoke(ctx context.Context, params *FileWriteParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if params.DryRun {
		preview := map[string]any{
			"dryRun": true,
			"wouldWrite": map[string]any{
				"path":          params.Path,
				"branch":        params.Branch,
				"bytes":         len(params.Content),
				"commitMessage": params.CommitMessage,
			},
		}
		previewJSON, err := json.Marshal(preview)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal dry-run preview")
		}
		return string(previewJSON), nil
	}

	if err := confirm.RequireConfirmationForAction("write file", params.Confirmed); err != nil {
		return "", err
	}

	if len(params.Content) > maxFileWriteBytes {
		return "", errors.Errorf("content size %d exceeds maximum %d bytes", len(params.Content), maxFileWriteBytes)
	}

	clonePath_ := clonePath(t.cloneDir, params.Owner, params.Repo)
	if err := ensureCloneExists(clonePath_, params.Owner, params.Repo); err != nil {
		return "", err
	}

	if err := rejectDotGitPath(params.Path); err != nil {
		return "", err
	}

	fullPath, err := validateFilePath(clonePath_, params.Path)
	if err != nil {
		return "", err
	}

	// Resolve symlinks at every path component to prevent symlink-based
	// directory traversal. A malicious repo can contain symlinks that would
	// redirect file writes outside the clone directory.
	safePath, err := resolveSymlinkSafe(clonePath_, fullPath, true)
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

	repo, err := git.PlainOpen(clonePath_)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open cloned repository at %q; clone it first", clonePath_)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}

	branchRef := plumbing.NewBranchReferenceName(params.Branch)
	err = wt.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Keep:   true,
	})
	if err != nil {
		// Branch doesn't exist; create it from BaseBranch or HEAD
		var baseHash plumbing.Hash
		if params.BaseBranch != "" {
			h, resolveErr := repo.ResolveRevision(plumbing.Revision(params.BaseBranch))
			if resolveErr != nil {
				return "", errors.Wrapf(resolveErr, "failed to resolve base branch %q for creating branch %q", params.BaseBranch, params.Branch)
			}
			baseHash = *h
		} else {
			head, headErr := repo.Head()
			if headErr != nil {
				return "", errors.Wrapf(headErr, "failed to get HEAD for creating branch %q", params.Branch)
			}
			baseHash = head.Hash()
		}

		err = wt.Checkout(&git.CheckoutOptions{
			Hash:   baseHash,
			Branch: branchRef,
			Create: true,
		})
		if err != nil {
			return "", errors.Wrapf(err, "failed to create and checkout branch %q", params.Branch)
		}
	}

	// Parent directories were already created by resolveSymlinkSafe (createDirs=true).
	// Write file using the symlink-safe path.
	if err := os.WriteFile(safePath, []byte(params.Content), 0o644); err != nil {
		return "", errors.Wrapf(err, "failed to write file %q", params.Path)
	}

	// Stage using the cleaned relative path (forward slashes for go-git).
	cleanRelPath, relErr := filepath.Rel(clonePath_, fullPath)
	if relErr != nil {
		return "", errors.Wrapf(relErr, "failed to compute relative path for %q", params.Path)
	}
	cleanRelPath = filepath.ToSlash(cleanRelPath)
	if _, err := wt.Add(cleanRelPath); err != nil {
		return "", errors.Wrapf(err, "failed to stage file %q", params.Path)
	}

	// Commit
	hash, err := wt.Commit(params.CommitMessage, &git.CommitOptions{
		Author: commitIdentity,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to commit")
	}

	// Push
	tok, err := t.token(params.Instance)
	if err != nil {
		return "", err
	}

	host, err := t.gitHost(params.Instance)
	if err != nil {
		return "", err
	}

	pushed := true
	err = repo.Push(&git.PushOptions{
		RemoteURL: fmt.Sprintf("https://%s/%s/%s.git", host, params.Owner, params.Repo),
		Auth:      &http.BasicAuth{Username: "x-access-token", Password: tok},
	})
	if err != nil {
		if err == git.NoErrAlreadyUpToDate {
			pushed = false
		} else {
			redactedErr := strings.ReplaceAll(err.Error(), tok, "***REDACTED***")
			return "", errors.Wrap(errors.New(redactedErr), "failed to push branch")
		}
	}

	output := &FileWriteOutput{
		Path:      params.Path,
		Branch:    params.Branch,
		CommitSHA: hash.String(),
		Pushed:    pushed,
	}

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

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
