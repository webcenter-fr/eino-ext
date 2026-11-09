# GitHub Tools (`components/tool/github`)

Eino tools for interacting with GitHub repositories, issues, pull requests, releases, branches, and webhooks.

Uses `go-github` (v71) for the GitHub REST API and `go-git` (v5) for local clone/branch operations.

## Libraries

| Library | Purpose |
|---------|---------|
| `github.com/google/go-github/v71` | GitHub REST API (issues, PRs, releases, repos, webhooks, search) |
| `github.com/go-git/go-git/v5` | Local clone, pull (fast-forward), branch creation |

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

`CloneDir` is the base directory for local clones. The LLM cannot pick an arbitrary path — clones always go under `<CloneDir>/<session>/<owner>/<repo>`.

### Session-scoped clones

The clone directory is namespaced per user session: `<CloneDir>/<session>/<owner>/<repo>`.
Concurrent sessions sharing one `Config.CloneDir` root therefore never collide. The session
ID is taken from the eino ADK session values, which the harness controls (the LLM cannot
spoof it):

```go
// ctx must be the current ADK agent run context (it carries the run session).
adk.AddSessionValue(ctx, github.CloneSessionKey, sessionID)
```

When the key is absent (plain `context.Background()`, unit tests, non-ADK usage), the
fallback segment `default` is used, yielding `<CloneDir>/default/<owner>/<repo>`.

Non-empty session ids are hashed to a fixed 16-hex-char segment
(`fileutil.SessionDirName`), so distinct ids never collide after sanitising.
Existing on-disk clone namespaces therefore move to the hashed names.

With `RequireSession: true` (a `Config` field, shared by all instances), a tool
call whose context carries no session id fails closed instead of falling back to
the `default` namespace. Multi-user hosts should enable it.

### Clone cleanup (GC)

Clone namespaces accumulate under `CloneDir` until they are cleaned up. Three
helpers manage the lifecycle:

```go
// StartCloneGC starts a background goroutine that, every interval, sweeps
// <CloneDir> for session namespaces whose mtime is older than ttl and removes
// them. ttl == 0 or interval <= 0 (or unusable configs) is a no-op; the
// goroutine stops on ctx cancellation.
github.StartCloneGC(ctx, configs, 1*time.Hour, 5*time.Minute)

// TouchCloneSession refreshes the current session's namespace mtime so
// StartCloneGC keeps it. Tools call it automatically on every read and on
// every authorized execute; it is never called on a dry-run (previews do not
// mutate). It is a no-op when the namespace does not exist.
github.TouchCloneSession(ctx, configs)

// CloneSessionDir returns the current session's namespace path
// <CloneDir>/<sessionSegment>.
dir, err := github.CloneSessionDir(ctx, configs)
```

A session that only reads, or whose last operation failed, is still kept alive
because every read tool touches the namespace after resolving the clone.

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
| `github_repo_clone` | Clone a repository to the local filesystem (plain read, no confirmation required) — `DryRun` still returns a preview |
| `github_repo_pull` | Update an existing clone to the latest remote state, non-destructively (fast-forward only; plain read, no confirmation required) — `DryRun` still returns a preview |
| `github_file_read` | Read file contents from a cloned repo (plain read, no confirmation required) |
| `github_file_search` | Grep (regex) within a cloned repo (plain read, no confirmation required) |
| `github_file_list` | List files/dirs in a cloned repo (plain read, no confirmation required) |
| `github_file_write` | Write/create a file in a cloned repo (local only, no commit/push, no confirmation required) — `DryRun` still returns a preview |
| `github_file_edit` | Replace an exact string in a cloned file (local only, no commit/push, no confirmation required) — `DryRun` previews the replacement count |
| `github_file_delete` | Delete a file or directory from a cloned repo (local only, no commit/push, no confirmation required) — `DryRun` still returns a preview |
| `github_file_copy` | Copy a file or directory within a cloned repo (local only, no commit/push, no confirmation required) — `DryRun` still returns a preview |
| `github_file_move` | Move/rename a file or directory within a cloned repo (local only, no commit/push, no confirmation required) — `DryRun` still returns a preview |
| `github_branch_checkout` | Check out an existing local branch, or create it from a base branch (local only, no confirmation required) — `DryRun` still returns a preview |
| `github_status` | Report the current branch, clean state, and changed files (local only, no confirmation required) |
| `github_diff` | Return the staged + unstaged diff relative to HEAD, bounded with a `truncated` flag (local only, no confirmation required) |

### Write Tools

| Tool | Description |
|------|-------------|
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
| `github_commit_push` | Stage, commit, and push local clone changes in one gated action (dry-run returns a diffstat) |

### File Tools

File tools operate on repositories cloned by `github_repo_clone`. The workflow is:

1. `github_repo_clone` — clone the repo to <CloneDir>/<session>/<owner>/<repo>
2. `github_file_read` / `github_file_search` / `github_file_list` — inspect
3. `github_branch_checkout` (optional) — check out or create a working branch
4. `github_file_write` / `github_file_edit` / `github_file_delete` / `github_file_copy` / `github_file_move` — modify files locally
5. `github_status` / `github_diff` — review the pending changes
6. `github_commit_push` — stage, commit, and push once (gated)
7. `github_pr_create` — open a PR from the pushed branch

`github_repo_pull` refreshes an existing clone to the latest remote state without
destroying local work: it fails on a dirty worktree, only fast-forwards (local
commits ahead of the remote are preserved and reported as an error), and never
resets, stashes, or force-updates. Use it to re-sync after others have pushed,
instead of re-cloning.

All local file tools (`github_file_read`, `github_file_search`,
`github_file_list`, `github_file_write`, `github_file_edit`,
`github_file_delete`, `github_file_copy`, `github_file_move`,
`github_branch_checkout`, `github_status`, `github_diff`) execute without the
confirmation gate; `DryRun=true` remains available on the mutating tools for a
no-mutation preview. `github_commit_push` is the single gated action that
persists changes.

All file paths are validated to stay within the clone directory. Symlinks and
the `.git` directory are always skipped or rejected.

Note the behavior and risk: `github_file_delete` and `github_file_move` remove
local paths in the clone, `github_file_copy` and `github_file_move` overwrite
existing local destinations, and `github_commit_push` commits and pushes the
selected branch (without force-pushing).

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

- **Path safety**: Clone target always under `Config.CloneDir`; session ids are hashed and owner/repo segments are sanitized (no traversal). `RequireSession` fails closed when no session id is present.
- **SSRF protection**: Webhook URLs must use HTTPS; loopback/private/metadata IPs are blocked.
- **Secret redaction**: GitHub tokens are redacted from all tool output.
- **Confirmation gating**: The 12 GitHub API/branch/commit write tools (`github_branch_create`, `github_release_create`, `github_issue_create`, `github_issue_comment`, `github_pr_create`, `github_pr_comment`, `github_pr_review`, `github_pr_suggest_change`, `github_pr_request_reviewers`, `github_repo_settings_update`, `github_webhook_upsert`, `github_commit_push`) require `Confirmed=true` and host authorization (or `DryRun=true` to preview). Repository clone/pull and local file tools (`github_file_read`, `github_file_search`, `github_file_list`, `github_file_write`, `github_file_edit`, `github_file_delete`, `github_file_copy`, `github_file_move`, `github_branch_checkout`, `github_status`, `github_diff`) are not gated and execute without confirmation; they only mutate the local clone. `github_commit_push` is the single gated action that commits and pushes.
- **Timeouts**: Every API call bounded by `Config.Timeout` (default 30s).
- **Pagination caps**: List/search tools cap results to prevent resource exhaustion.
- **Copy size limits**: Directory copy/move is capped by `Config.MaxCopyBytes`
  (default 50MB); when the limit is exceeded, the partial destination is removed
  unless it pre-existed (a merged destination is left intact).

## Prompts

This package does not include system prompts. For the PR-reviewer persona, use:

```go
prReviewPrompt := prompt.NewPullRequestReview(projectRules)
```

from `components/prompt` which is designed to work with these GitHub tools (`github_pr_get`, `github_pr_review`, `github_pr_suggest_change`, `github_pr_request_reviewers`).

## Alternative: MCP

This repository also supports the MCP protocol via `components/tool/mcp`. Users running an MCP-capable host can use the official GitHub MCP server (`github/github-mcp-server`) as an alternative for API-only operations. The native tools in this package provide additional capabilities (local clone/branch via go-git) and tighter security controls.
