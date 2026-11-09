package github

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitdiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/goccy/go-json"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

const commitPushDescription = `
** General Purpose **
It stages local changes in a previously cloned GitHub repository, commits them,
and pushes the branch to the remote. This is the single gated action that
persists a batch of file edits.

** Output **
- DryRun: {branch, remoteBranch, commitMessage, files:[{path, status, insertions, deletions}], diffstat, truncated}
- Execute: {branch, commitSha, committed, pushed, files}

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- The branch is checked out first; if it does not exist locally it is created from baseBranch (default: current HEAD).
- All changes are staged, or only the given paths when paths is set. The .git directory is never staged.
- Never force-pushes: a non-fast-forward rejection is an error.
- Real execution requires confirmed=true and host authorization. Use DryRun=true to preview.
`

// CommitPushParams defines the parameters for committing and pushing local changes.
type CommitPushParams struct {
	Instance      string   `json:"instance"      validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner         string   `json:"owner"         validate:"required" jsonschema:"(required) Repository owner."`
	Repo          string   `json:"repo"          validate:"required" jsonschema:"(required) Repository name."`
	Branch        string   `json:"branch"        validate:"required" jsonschema:"(required) Branch to commit and push."`
	BaseBranch    string   `json:"baseBranch,omitempty" jsonschema:"(optional) Base branch or commit SHA used when creating the branch. Defaults to current HEAD."`
	CommitMessage string   `json:"commitMessage" validate:"required" jsonschema:"(required) Git commit message."`
	Paths         []string `json:"paths,omitempty" validate:"omitempty" jsonschema:"(optional) Relative paths to stage. Omit to stage all changes."`
	DryRun        bool     `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the commit and push without side effects."`
	Confirmed     bool     `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually commit and push. Set this after the user has approved the dry-run result."`
}

// CommitPushFile describes one changed file in a commit/push preview.
type CommitPushFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// CommitPushDryRunOutput is the no-side-effect preview of a commit/push.
type CommitPushDryRunOutput struct {
	Branch        string           `json:"branch"`
	RemoteBranch  string           `json:"remoteBranch"`
	CommitMessage string           `json:"commitMessage"`
	Files         []CommitPushFile `json:"files"`
	Diffstat      string           `json:"diffstat"`
	Truncated     bool             `json:"truncated"`
}

// CommitPushOutput is the result of a real commit/push.
type CommitPushOutput struct {
	Branch    string           `json:"branch"`
	CommitSHA string           `json:"commitSha,omitempty"`
	Committed bool             `json:"committed"`
	Pushed    bool             `json:"pushed"`
	Files     []CommitPushFile `json:"files,omitempty"`
}

// CommitPushTool is the gated eino tool that commits and pushes local changes.
type CommitPushTool struct {
	*baseTool
	tool.InvokableTool
}

const defaultCommitPushMaxFiles = 1000

// Invoke stages, commits, and pushes the requested branch.
func (t *CommitPushTool) Invoke(ctx context.Context, params *CommitPushParams) (string, error) {
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

	if params.DryRun {
		return t.dryRun(clonePath_, params)
	}

	if err := confirm.RequireConfirmationForActionCtx(ctx, "github_commit_push", "commit and push", params.Confirmed); err != nil {
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

	if _, err := checkoutBranch(repo, wt, params.Branch, params.BaseBranch, true); err != nil {
		return "", err
	}

	if err := stageChanges(wt, clonePath_, params.Paths); err != nil {
		return "", err
	}

	status, err := wt.Status()
	if err != nil {
		return "", errors.Wrap(err, "failed to read worktree status")
	}
	files := statusFiles(clonePath_, repo, status, defaultCommitPushMaxFiles, true)

	output := &CommitPushOutput{
		Branch: params.Branch,
		Files:  files,
	}

	if hasStagedChanges(status) {
		hash, commitErr := wt.Commit(params.CommitMessage, &git.CommitOptions{Author: commitIdentity})
		if commitErr != nil {
			if errors.Is(commitErr, git.ErrEmptyCommit) {
				output.Committed = false
			} else {
				return "", errors.Wrap(commitErr, "failed to commit")
			}
		} else {
			output.Committed = true
			output.CommitSHA = hash.String()
		}
	}

	pushed, err := t.push(ctx, repo, params)
	if err != nil {
		return "", err
	}
	output.Pushed = pushed

	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

func (t *CommitPushTool) dryRun(clonePath_ string, params *CommitPushParams) (string, error) {
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

	files := statusFiles(clonePath_, repo, status, defaultCommitPushMaxFiles, false)
	insertions, deletions := 0, 0
	for _, file := range files {
		insertions += file.Insertions
		deletions += file.Deletions
	}

	output := &CommitPushDryRunOutput{
		Branch:        params.Branch,
		RemoteBranch:  "refs/heads/" + params.Branch,
		CommitMessage: params.CommitMessage,
		Files:         files,
		Diffstat:      fmt.Sprintf("%d file(s) changed, %d insertion(s)(+), %d deletion(s)(-)", len(files), insertions, deletions),
		Truncated:     len(status) > len(files),
	}
	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(result), nil
}

func (t *CommitPushTool) push(ctx context.Context, repo *git.Repository, params *CommitPushParams) (bool, error) {
	tok, err := t.token(params.Instance)
	if err != nil {
		return false, err
	}
	pushURL, err := t.pushURL(params.Instance, params.Owner, params.Repo)
	if err != nil {
		return false, err
	}

	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteURL: pushURL,
		Auth:      &http.BasicAuth{Username: "x-access-token", Password: tok},
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", params.Branch, params.Branch)),
		},
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, git.NoErrAlreadyUpToDate):
		return false, nil
	case errors.Is(err, git.ErrNonFastForwardUpdate):
		return false, errors.Errorf("push of branch %q was rejected as non-fast-forward; pull/rebase and retry (force-push is never used)", params.Branch)
	default:
		msg := err.Error()
		// Guard against an empty token: strings.ReplaceAll(s, "", x) would
		// insert the marker between every character and garble the message.
		if tok != "" {
			msg = strings.ReplaceAll(msg, tok, "***REDACTED***")
		}
		return false, errors.Wrap(errors.New(msg), "failed to push branch")
	}
}

func stageChanges(wt *git.Worktree, clonePath_ string, paths []string) error {
	if len(paths) == 0 {
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			return errors.Wrap(err, "failed to stage all changes")
		}
		return nil
	}

	for _, path := range paths {
		if err := fileutil.RejectDotGitPath(path); err != nil {
			return err
		}
		fullPath, err := fileutil.ValidateRelativePath(clonePath_, path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(clonePath_, fullPath)
		if err != nil {
			return errors.Wrapf(err, "failed to compute relative path for %q", path)
		}
		if _, err := wt.Add(filepath.ToSlash(relPath)); err != nil {
			return errors.Wrapf(err, "failed to stage %q", path)
		}
	}
	return nil
}

func hasStagedChanges(status git.Status) bool {
	for _, fileStatus := range status {
		if fileStatus.Staging != git.Unmodified {
			return true
		}
	}
	return false
}

func statusFiles(clonePath_ string, repo *git.Repository, status git.Status, maxFiles int, stagedOnly bool) []CommitPushFile {
	paths := make([]string, 0, len(status))
	for path, fileStatus := range status {
		if stagedOnly && fileStatus.Staging == git.Unmodified {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var headTree *object.Tree
	if head, err := repo.Head(); err == nil {
		if commit, commitErr := repo.CommitObject(head.Hash()); commitErr == nil {
			if tree, treeErr := commit.Tree(); treeErr == nil {
				headTree = tree
			}
		}
	}

	files := make([]CommitPushFile, 0, min(len(paths), maxFiles))
	for _, path := range paths {
		if len(files) >= maxFiles {
			break
		}
		oldContent, newContent, note := diffContents(clonePath_, headTree, path)
		insertions, deletions := 0, 0
		if note == "" {
			insertions, deletions = countDiffLines(oldContent, newContent)
		}
		files = append(files, CommitPushFile{
			Path:       path,
			Status:     fileStatusString(status[path]),
			Insertions: insertions,
			Deletions:  deletions,
		})
	}
	return files
}

func countDiffLines(oldContent, newContent string) (insertions, deletions int) {
	for _, d := range gitdiff.Do(oldContent, newContent) {
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			insertions += countLines(d.Text)
		case diffmatchpatch.DiffDelete:
			deletions += countLines(d.Text)
		}
	}
	return insertions, deletions
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

// NewCommitPushTool creates a new CommitPushTool.
func NewCommitPushTool(ctx context.Context, configs Configs) (*CommitPushTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newCommitPushTool(ctx, base)
}

func newCommitPushTool(ctx context.Context, base *baseTool) (*CommitPushTool, error) {
	commitTool := &CommitPushTool{baseTool: base}
	t, err := utils.InferTool("github_commit_push", commitPushDescription, commitTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	commitTool.InvokableTool = t
	return commitTool, nil
}
