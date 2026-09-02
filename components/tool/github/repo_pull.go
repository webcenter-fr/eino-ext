package github

import (
	"context"
	"fmt"
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

const repoPullDescription = `
** General Purpose **
It updates an already-cloned GitHub repository to the latest state of a remote
branch WITHOUT destroying local work. This is the non-destructive alternative to
re-cloning.

** Output **
It returns the local path, branch, previous HEAD commit, and new HEAD commit
(or a note that it is already up to date).

** Important **
- The repository must already be cloned (run github_repo_clone first).
- Local uncommitted changes cause an error; nothing is discarded.
- Only fast-forward updates are performed. Local commits not present on the
  remote cause an error; nothing is discarded.
- The clone lives under <CloneDir>/<session>/<owner>/<repo>.
`

// RepoPullParams defines the parameters for pulling updates into a cloned GitHub repository.
type RepoPullParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Branch    string `json:"branch,omitempty" jsonschema:"(optional) Branch to update. Defaults to the currently checked-out branch."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the pull without making changes."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set after approving the dry-run result."`
}

// RepoPullTool is an eino tool for updating an already-cloned GitHub repository
// to the latest remote state, non-destructively (fast-forward only).
type RepoPullTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke pulls the latest remote state into the cloned repository.
func (t *RepoPullTool) Invoke(ctx context.Context, params *RepoPullParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	targetPath := t.clonePathForSession(ctx, params.Owner, params.Repo)

	if params.DryRun {
		return t.dryRun(ctx, params, targetPath)
	}

	if err := confirm.RequireConfirmationForAction("pull", params.Confirmed); err != nil {
		return "", err
	}

	tok, err := t.token(params.Instance)
	if err != nil {
		return "", err
	}

	repo, err := git.PlainOpen(targetPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to open cloned repository at %q; run github_repo_clone first", targetPath)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}

	status, err := wt.Status()
	if err != nil {
		return "", errors.Wrap(err, "failed to read worktree status")
	}
	if !status.IsClean() {
		return "", errors.Errorf(
			"clone at %q has uncommitted changes; commit, push, or stash them first. pull is non-destructive and never discards local work",
			targetPath)
	}

	branch, err := resolvePullBranch(repo, params.Branch)
	if err != nil {
		return "", err
	}

	before, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get HEAD before pull")
	}
	beforeHash := before.Hash()

	var progressBuf strings.Builder
	// PullContext (not Pull) so the invocation context is honored: Pull wraps
	// context.Background(), which would make a hung network fetch uncancellable.
	err = wt.PullContext(ctx, &git.PullOptions{
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Force:         false, // non-destructive: fast-forward only
		Auth:          &http.BasicAuth{Username: "x-access-token", Password: tok},
		Progress:      &progressBuf,
	})
	switch {
	case err == nil:
		// fallthrough to result
	case errors.Is(err, git.NoErrAlreadyUpToDate):
		// not an error; already up to date
	case errors.Is(err, git.ErrNonFastForwardUpdate):
		return "", errors.Errorf(
			"local branch %q has commits not present on the remote (or has diverged); a pull would lose local work. Reconcile manually or re-clone if you intend to discard.",
			branch)
	default:
		redacted := strings.ReplaceAll(err.Error(), tok, "***REDACTED***")
		return "", errors.Wrap(errors.New(redacted), "failed to pull branch")
	}

	after, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get HEAD after pull")
	}
	afterHash := after.Hash()

	return fmt.Sprintf(
		`{"pulled": true, "alreadyUpToDate": %t, "path": %q, "branch": %q, "previousHead": %q, "headCommit": %q}`,
		beforeHash == afterHash, targetPath, branch, beforeHash.String(), afterHash.String()), nil
}

// resolvePullBranch returns the branch to pull: params.Branch if set, otherwise
// the currently checked-out branch. A detached HEAD without an explicit branch is
// an error.
func resolvePullBranch(repo *git.Repository, branch string) (string, error) {
	if branch != "" {
		return branch, nil
	}
	head, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get HEAD")
	}
	if !head.Name().IsBranch() {
		return "", errors.Errorf("HEAD is detached (%s); specify the 'branch' parameter to pull", head.Hash().String())
	}
	return head.Name().Short(), nil
}

// dryRun builds a read-only preview of the pull. It does not fetch or mutate.
func (t *RepoPullTool) dryRun(ctx context.Context, params *RepoPullParams, targetPath string) (string, error) {
	preview := map[string]any{
		"dryRun": true,
		"wouldPull": map[string]any{
			"path":   targetPath,
			"branch": params.Branch,
			"owner":  params.Owner,
			"repo":   params.Repo,
		},
	}
	// Best-effort read-only current-state reporting (ignored if the clone is absent).
	if repo, err := git.PlainOpen(targetPath); err == nil {
		would := preview["wouldPull"].(map[string]any)
		if wt, wtErr := repo.Worktree(); wtErr == nil {
			if status, stErr := wt.Status(); stErr == nil {
				would["dirty"] = !status.IsClean()
			}
		}
		if head, hErr := repo.Head(); hErr == nil {
			would["headCommit"] = head.Hash().String()
			would["headRef"] = head.Name().String()
		}
	}
	b, err := json.Marshal(preview)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal dry-run preview")
	}
	return string(b), nil
}

// NewRepoPullTool creates a new RepoPullTool.
func NewRepoPullTool(ctx context.Context, configs Configs) (*RepoPullTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newRepoPullTool(ctx, base)
}

func newRepoPullTool(ctx context.Context, base *baseTool) (*RepoPullTool, error) {
	pullTool := &RepoPullTool{baseTool: base}
	t, err := utils.InferTool("github_repo_pull", repoPullDescription, pullTool.Invoke,
		utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	pullTool.InvokableTool = t
	return pullTool, nil
}

var _ tool.InvokableTool = (*RepoPullTool)(nil)
