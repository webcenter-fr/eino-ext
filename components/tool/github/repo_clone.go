package github

import (
	"context"
	"fmt"
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const repoCloneDescription = `
** General Purpose **
It clones a GitHub repository to the local filesystem under the configured clone directory.

** Output **
It returns the local path where the repository was cloned and the HEAD commit information.

** Important **
- The clone path is fixed at tool creation time. The LLM cannot choose an arbitrary path.
- Repository is cloned under: <CloneDir>/<owner>/<repo>
`

// RepoCloneParams defines the parameters for cloning a GitHub repository.
type RepoCloneParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner     string `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo      string `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Branch    string `json:"branch,omitempty" jsonschema:"(optional) Branch to checkout. Defaults to the default branch."`
	Depth     int    `json:"depth,omitempty" jsonschema:"(optional) Clone depth (shallow clone). 0 = full clone."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, return the resolved path without cloning."`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the clone. Set this after the user has approved the dry-run result."`
}

// RepoCloneTool is an eino tool for cloning GitHub repositories.
type RepoCloneTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke clones a GitHub repository.
func (t *RepoCloneTool) Invoke(ctx context.Context, params *RepoCloneParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	targetPath := clonePath(t.cloneDir, params.Owner, params.Repo)

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldCloneTo": %q, "owner": %q, "repo": %q, "branch": %q}`, targetPath, params.Owner, params.Repo, params.Branch), nil
	}

	if err := confirm.RequireConfirmationForAction("clone", params.Confirmed); err != nil {
		return "", err
	}

	tok, err := t.token(params.Instance)
	if err != nil {
		return "", err
	}

	_ = os.RemoveAll(targetPath)

	host, err := t.gitHost(params.Instance)
	if err != nil {
		return "", err
	}
	cloneURL := fmt.Sprintf("https://%s/%s/%s.git", host, params.Owner, params.Repo)

	cloneOpts := &git.CloneOptions{
		URL:             cloneURL,
		Auth:            &http.BasicAuth{Username: "x-access-token", Password: tok},
		InsecureSkipTLS: false,
		SingleBranch:    params.Branch != "",
	}

	if params.Branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(params.Branch)
	}

	if params.Depth > 0 {
		cloneOpts.Depth = params.Depth
	}

	var progressBuf strings.Builder
	cloneOpts.Progress = &progressBuf

	repo, err := git.PlainCloneContext(ctx, targetPath, false, cloneOpts)
	if err != nil {
		redactedErr := strings.ReplaceAll(err.Error(), tok, "***REDACTED***")
		return "", errors.Wrap(errors.New(redactedErr), "failed to clone repository")
	}

	head, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get HEAD after clone")
	}

	return fmt.Sprintf(`{"clonedTo": %q, "headCommit": %q, "headRef": %q}`, targetPath, head.Hash().String(), head.Name().String()), nil
}

// NewRepoCloneTool creates a new RepoCloneTool.
func NewRepoCloneTool(ctx context.Context, configs Configs) (*RepoCloneTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newRepoCloneTool(ctx, base)
}

func newRepoCloneTool(ctx context.Context, base *baseTool) (*RepoCloneTool, error) {
	cloneTool := &RepoCloneTool{baseTool: base}
	t, err := utils.InferTool("github_repo_clone", repoCloneDescription, cloneTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	cloneTool.InvokableTool = t

	return cloneTool, nil
}
