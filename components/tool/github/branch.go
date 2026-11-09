package github

import (
	"emperror.dev/errors"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// resolveBaseHash resolves the commit a new branch should start from. An empty
// baseBranch means the current HEAD, matching the documented default-branch
// behaviour.
func resolveBaseHash(repo *git.Repository, baseBranch string) (plumbing.Hash, error) {
	if baseBranch != "" {
		hash, err := repo.ResolveRevision(plumbing.Revision(baseBranch))
		if err != nil {
			return plumbing.ZeroHash, errors.Wrapf(err, "failed to resolve base branch %q", baseBranch)
		}
		return *hash, nil
	}
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, errors.Wrap(err, "failed to get HEAD")
	}
	return head.Hash(), nil
}

// checkoutBranch checks out an existing local branch, or creates it from
// baseBranch (default: current HEAD) when create is true. It returns the commit
// the branch points at after the operation.
func checkoutBranch(repo *git.Repository, wt *git.Worktree, branch, baseBranch string, create bool) (plumbing.Hash, error) {
	branchRef := plumbing.NewBranchReferenceName(branch)

	if _, err := repo.Reference(branchRef, false); err == nil {
		if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Keep: true}); err != nil {
			return plumbing.ZeroHash, errors.Wrapf(err, "failed to checkout branch %q", branch)
		}
		head, err := repo.Head()
		if err != nil {
			return plumbing.ZeroHash, errors.Wrapf(err, "failed to get HEAD after checking out branch %q", branch)
		}
		return head.Hash(), nil
	} else if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash, errors.Wrapf(err, "failed to resolve branch %q", branch)
	}

	if !create {
		return plumbing.ZeroHash, errors.Errorf("branch %q does not exist; set create=true to create it", branch)
	}

	baseHash, err := resolveBaseHash(repo, baseBranch)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Hash:   baseHash,
		Branch: branchRef,
		Create: true,
	}); err != nil {
		return plumbing.ZeroHash, errors.Wrapf(err, "failed to create and checkout branch %q", branch)
	}
	return baseHash, nil
}
