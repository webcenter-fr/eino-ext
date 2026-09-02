# GitHub Tools (`components/tool/github`)

Eino tools for interacting with GitHub repositories, issues, pull requests, releases, branches, and webhooks.

Uses `go-github` (v71) for the GitHub REST API and `go-git` (v5) for local clone/branch operations.

## Libraries

| Library | Purpose |
|---------|---------|
| `github.com/google/go-github/v71` | GitHub REST API (issues, PRs, releases, repos, webhooks, search) |
| `github.com/go-git/go-git/v5` | Local clone, branch creation |

## Configuration

```go
configs := github.Configs{
    "default": github.Config{
        Token:    os.Getenv("GITHUB_TOKEN"),
        CloneDir: "/tmp/github-work",   // fixed at creation, NOT chosen by the LLM
        BaseURL:  "",                   // empty = github.com; set for GHES
    },
}
```

`CloneDir` is the base directory for local clones. The LLM cannot pick an arbitrary path — clones always go under `<CloneDir>/<owner>/<repo>`.

## Tools

### Read Tools

| Tool | Description |
|------|-------------|
| `github_issue_list` | List issues in a repository |
| `github_issue_get` | Get issue details |
| `github_pr_list` | List pull requests in a repository |
| `github_pr_get` | Get pull request details |
| `github_org_repo_list` | List repositories in an organization |
| `github_repo_search` | Search repositories by query |
| `github_file_read` | Read file contents from a cloned repo |
| `github_file_search` | Grep (regex) within a cloned repo |
| `github_file_list` | List files/dirs in a cloned repo |

### Write Tools

| Tool | Description |
|------|-------------|
| `github_repo_clone` | Clone a repository to the local filesystem |
| `github_branch_create` | Create a branch (local via go-git, or remote via API) |
| `github_release_create` | Create a release (with optional tag) |
| `github_issue_create` | Create an issue |
| `github_issue_comment` | Add a comment to an issue |
| `github_pr_create` | Create a pull request |
| `github_pr_comment` | Add a comment to a pull request |
| `github_pr_review` | Submit a review (APPROVE/REQUEST_CHANGES/COMMENT) |
| `github_pr_suggest_change` | Post an inline code suggestion |
| `github_pr_request_reviewers` | Request reviewers on a PR |
| `github_repo_settings_update` | Update repository settings |
| `github_webhook_upsert` | Create or update a repository webhook |
| `github_file_write` | Write/create a file, commit, and push |
| `github_file_delete` | Delete a file or directory from a cloned repo (local only, no commit/push) |
| `github_file_copy` | Copy a file or directory within a cloned repo (local only, no commit/push) |
| `github_file_move` | Move/rename a file or directory within a cloned repo (local only, no commit/push) |

### File Tools

File tools operate on repositories cloned by `github_repo_clone`. The workflow is:

1. `github_repo_clone` — clone the repo to <CloneDir>/<owner>/<repo>
2. `github_file_read` / `github_file_search` / `github_file_list` — inspect
3. `github_branch_create` (optional) — create a working branch
4. `github_file_write` / `github_file_delete` / `github_file_copy` / `github_file_move` — modify files locally
5. `github_file_write` — commit and push changes
6. `github_pr_create` — open a PR from the pushed branch

All file paths are validated to stay within the clone directory. Symlinks and
the `.git` directory are always skipped or rejected.

## Usage

```go
// Create all tools
tools, err := github.NewAllTools(ctx, configs)

// Create read-only tools only
readTools, err := github.NewReadOnlyTools(ctx, configs)

// Create with safety middleware
tools, mw, err := github.NewAllToolsWithSafety(ctx, configs, &safety.Config{
    Policy: myCELPolicy,
})
```

## Security

- **Path safety**: Clone target always under `Config.CloneDir`; owner/repo segments are sanitized.
- **SSRF protection**: Webhook URLs must use HTTPS; loopback/private/metadata IPs are blocked.
- **Secret redaction**: GitHub tokens are redacted from all tool output.
- **Confirmation gating**: All write tools require `Confirmed=true` (or use `DryRun` to preview).
- **Timeouts**: Every API call bounded by `Config.Timeout` (default 30s).
- **Pagination caps**: List/search tools cap results to prevent resource exhaustion.

## Prompts

This package does not include system prompts. For the PR-reviewer persona, use:

```go
prReviewPrompt := prompt.NewPullRequestReview(projectRules)
```

from `components/prompt` which is designed to work with these GitHub tools (`github_pr_get`, `github_pr_review`, `github_pr_suggest_change`, `github_pr_request_reviewers`).

## Alternative: MCP

This repository also supports the MCP protocol via `components/tool/mcp`. Users running an MCP-capable host can use the official GitHub MCP server (`github/github-mcp-server`) as an alternative for API-only operations. The native tools in this package provide additional capabilities (local clone/branch via go-git) and tighter security controls.
