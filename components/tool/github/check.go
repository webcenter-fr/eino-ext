package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const ghCheckTimeout = 10 * time.Second

// Check performs a health check against configured GitHub instances.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "github",
			Status:    checkup.StatusError,
			Error:     "no GitHub instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		func() {
			cfg := configs.GetConfig(instance)
			baseCtx, cancel := context.WithTimeout(ctx, ghCheckTimeout)

			client, err := NewClient(baseCtx, &cfg)
			if err != nil {
				all = append(all, clientErrorResults(instance, err)...)
				cancel()
				return
			}

			results := probeInstance(baseCtx, client, instance)
			all = append(all, results...)
			cancel()
		}()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	return checkup.Results{
		{Component: "github_repo_search", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "github_issue_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "github_issue_get", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "github_pr_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "github_pr_get", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "github_org_repo_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

func probeInstance(ctx context.Context, client *github.Client, instance string) checkup.Results {
	var results checkup.Results

	rr, repos, err := probeRepoSearch(ctx, client, instance)
	results = append(results, rr)
	if err != nil || len(repos) == 0 {
		limitedMsg := ""
		if err == nil {
			limitedMsg = "no repositories to discover"
		}
		results = append(results, discoveryChainLimited(instance, limitedMsg)...)
		return results
	}

	fullName := repos[0].GetFullName()
	parts := strings.SplitN(fullName, "/", 2)
	owner, repo := parts[0], parts[1]

	if len(parts) != 2 || owner == "" || repo == "" {
		results = append(results, discoveryChainLimited(instance, "invalid fullName from search")...)
		return results
	}

	ir, issues, err := probeIssueList(ctx, client, instance, owner, repo)
	results = append(results, ir)
	if err == nil && len(issues) > 0 {
		results = append(results, probeIssueGet(ctx, client, instance, owner, repo, issues[0].GetNumber()))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "github_issue_get",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no issues to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "github_issue_get",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	prr, prs, err := probePRList(ctx, client, instance, owner, repo)
	results = append(results, prr)
	if err == nil && len(prs) > 0 {
		results = append(results, probePRGet(ctx, client, instance, owner, repo, prs[0].GetNumber()))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "github_pr_get",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no pull requests to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "github_pr_get",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	results = append(results, probeOrgRepoList(ctx, client, instance, owner))

	return results
}

func probeRepoSearch(ctx context.Context, client *github.Client, instance string) (checkup.Result, []*github.Repository, error) {
	result_, _, err := client.Search.Repositories(ctx, "stars:>100", &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return checkup.Result{
			Component: "github_repo_search",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to search repositories").Error(),
		}, nil, err
	}
	count := 0
	if result_ != nil {
		count = result_.GetTotal()
	}
	msg := fmt.Sprintf("%d repositories found, RBAC ok", count)
	return checkup.Result{
		Component: "github_repo_search",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, result_.Repositories, nil
}

func probeIssueList(ctx context.Context, client *github.Client, instance, owner, repo string) (checkup.Result, []*github.Issue, error) {
	issues, _, err := client.Issues.ListByRepo(ctx, owner, repo, &github.IssueListByRepoOptions{
		State:       "all",
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return checkup.Result{
			Component: "github_issue_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list issues").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d issues found, RBAC ok", len(issues))
	return checkup.Result{
		Component: "github_issue_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, issues, nil
}

func probeIssueGet(ctx context.Context, client *github.Client, instance, owner, repo string, number int) checkup.Result {
	_, _, err := client.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return checkup.Result{
			Component: "github_issue_get",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get issue").Error(),
		}
	}
	return checkup.Result{
		Component: "github_issue_get",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("got issue #%d, RBAC ok", number),
	}
}

func probePRList(ctx context.Context, client *github.Client, instance, owner, repo string) (checkup.Result, []*github.PullRequest, error) {
	prs, _, err := client.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
		State:       "all",
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return checkup.Result{
			Component: "github_pr_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list pull requests").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d pull requests found, RBAC ok", len(prs))
	return checkup.Result{
		Component: "github_pr_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, prs, nil
}

func probePRGet(ctx context.Context, client *github.Client, instance, owner, repo string, number int) checkup.Result {
	_, _, err := client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return checkup.Result{
			Component: "github_pr_get",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get pull request").Error(),
		}
	}
	return checkup.Result{
		Component: "github_pr_get",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("got PR #%d, RBAC ok", number),
	}
}

func probeOrgRepoList(ctx context.Context, client *github.Client, instance, org string) checkup.Result {
	repos, _, err := client.Repositories.ListByOrg(ctx, org, &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return checkup.Result{
			Component: "github_org_repo_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list org repositories").Error(),
		}
	}
	msg := fmt.Sprintf("%d org repos found, RBAC ok", len(repos))
	return checkup.Result{
		Component: "github_org_repo_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}
}

func discoveryChainLimited(instance, msg string) checkup.Results {
	if msg == "" {
		msg = "no repositories to discover"
	}
	return checkup.Results{
		{Component: "github_issue_list", Instance: instance, Status: checkup.StatusLimited, Message: msg},
		{Component: "github_issue_get", Instance: instance, Status: checkup.StatusLimited, Message: msg},
		{Component: "github_pr_list", Instance: instance, Status: checkup.StatusLimited, Message: msg},
		{Component: "github_pr_get", Instance: instance, Status: checkup.StatusLimited, Message: msg},
		{Component: "github_org_repo_list", Instance: instance, Status: checkup.StatusLimited, Message: msg},
	}
}
