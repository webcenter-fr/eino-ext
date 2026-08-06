package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type GitHubToolTestSuite struct {
	suite.Suite
	server *httptest.Server
}

func TestGitHubToolSuite(t *testing.T) {
	suite.Run(t, new(GitHubToolTestSuite))
}

func (s *GitHubToolTestSuite) SetupSuite() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case strings.Contains(path, "/search/repositories"):
			page := r.URL.Query().Get("page")
			w.Header().Set("Content-Type", "application/vnd.github.v3+json")
			if page == "2" {
				w.Header().Set("Link", `<https://api.github.com/search/repositories?page=2>; rel="last"`)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"total_count": 2, "incomplete_results": false, "items": [{"name": "searched-repo-2", "full_name": "testorg/searched-repo-2", "description": "Search result 2", "language": "Python", "private": false, "stargazers_count": 3, "open_issues_count": 1, "default_branch": "develop", "html_url": "https://github.com/testorg/searched-repo-2"}]`))
				return
			}
			w.Header().Set("Link", `<https://api.github.com/search/repositories?page=2>; rel="next", <https://api.github.com/search/repositories?page=2>; rel="last"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"total_count": 2, "incomplete_results": false, "items": [{"name": "searched-repo", "full_name": "testorg/searched-repo", "description": "Search result", "language": "Go", "private": false, "stargazers_count": 5, "open_issues_count": 0, "default_branch": "main", "html_url": "https://github.com/testorg/searched-repo"}]`))
		case strings.HasSuffix(path, "/comments") && strings.Contains(path, "/issues/1/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 100, "html_url": "https://github.com/testowner/testrepo/issues/1#issuecomment-100"}`))
		case strings.HasSuffix(path, "/issues/1") && strings.Contains(path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"number": 1, "title": "Test issue", "state": "open",
				"user": {"login": "testuser"}, "body": "Issue body",
				"labels": [{"name": "bug"}], "assignees": [{"login": "assignee1"}],
				"milestone": {"title": "v1.0"},
				"created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-02T00:00:00Z",
				"html_url": "https://github.com/testowner/testrepo/issues/1"
			}`))
		case strings.HasSuffix(path, "/issues") && strings.Contains(path, "/repos/"):
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"number": 3, "title": "New issue", "state": "open", "html_url": "https://github.com/testowner/testrepo/issues/3"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"number": 1, "title": "Test issue", "state": "open", "user": {"login": "testuser"}, "labels": [{"name": "bug"}], "created_at": "2025-01-01T00:00:00Z", "updated_at": "2025-01-02T00:00:00Z", "html_url": "https://github.com/testowner/testrepo/issues/1"},
				{"number": 2, "title": "Feature request", "state": "closed", "user": {"login": "otheruser"}, "labels": [], "created_at": "2025-02-01T00:00:00Z", "updated_at": "2025-02-02T00:00:00Z", "html_url": "https://github.com/testowner/testrepo/issues/2"}
			]`))
		case strings.HasSuffix(path, "/pulls/10/reviews"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 300, "state": "APPROVED", "html_url": "https://github.com/testowner/testrepo/pull/10#pullrequestreview-300"}`))
		case strings.Contains(path, "/pulls/10/comments"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 400, "path": "main.go", "line": 42, "html_url": "https://github.com/testowner/testrepo/pull/10#discussion_r400"}`))
		case strings.HasSuffix(path, "/pulls/10/requested_reviewers"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number": 10, "requested_reviewers": [{"login": "reviewer1"}, {"login": "reviewer2"}]}`))
		case strings.HasSuffix(path, "/pulls/10"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"number": 10, "title": "Test PR", "state": "open",
				"user": {"login": "pruser"}, "body": "PR body",
				"head": {"ref": "feature-branch"}, "base": {"ref": "main"},
				"labels": [{"name": "enhancement"}], "assignees": [{"login": "reviewer1"}],
				"mergeable": true, "draft": false,
				"created_at": "2025-03-01T00:00:00Z", "updated_at": "2025-03-02T00:00:00Z",
				"html_url": "https://github.com/testowner/testrepo/pull/10"
			}`))
		case strings.Contains(path, "/issues/10/comments"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 200, "html_url": "https://github.com/testowner/testrepo/pull/10#issuecomment-200"}`))
		case strings.HasSuffix(path, "/pulls") && strings.Contains(path, "/repos/"):
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"number": 11, "title": "New PR", "state": "open", "html_url": "https://github.com/testowner/testrepo/pull/11", "draft": false}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"number": 10, "title": "Test PR", "state": "open", "user": {"login": "pruser"}, "head": {"ref": "feature-branch"}, "base": {"ref": "main"}, "draft": false, "created_at": "2025-03-01T00:00:00Z", "updated_at": "2025-03-02T00:00:00Z", "html_url": "https://github.com/testowner/testrepo/pull/10"}]`))
		case strings.HasPrefix(path, "/api/v3/orgs/") && strings.HasSuffix(path, "/repos"):
			page := r.URL.Query().Get("page")
			w.Header().Set("Content-Type", "application/json")
			if page == "2" {
				w.Header().Set("Link", `<https://api.github.com/orgs/testorg/repos?page=2>; rel="last"`)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"name": "repo2", "full_name": "testorg/repo2", "description": "Second repo", "language": "Python", "private": false, "stargazers_count": 5, "open_issues_count": 1, "default_branch": "develop", "html_url": "https://github.com/testorg/repo2"}]`))
				return
			}
			w.Header().Set("Link", `<https://api.github.com/orgs/testorg/repos?page=2>; rel="next", <https://api.github.com/orgs/testorg/repos?page=2>; rel="last"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"name": "repo1", "full_name": "testorg/repo1", "description": "First repo", "language": "Go", "private": false, "stargazers_count": 10, "open_issues_count": 2, "default_branch": "main", "html_url": "https://github.com/testorg/repo1"}]`))
		case strings.HasSuffix(path, "/repos/testowner/testrepo/hooks/500"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 500, "events": ["push", "pull_request"], "active": true}`))
		case strings.HasSuffix(path, "/hooks") && strings.Contains(path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 500, "events": ["push"], "active": true}`))
		case strings.HasSuffix(path, "/releases") && strings.Contains(path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 600, "tag_name": "v1.0.0", "name": "v1.0.0 Release", "html_url": "https://github.com/testowner/testrepo/releases/tag/v1.0.0", "draft": false, "prerelease": false}`))
		case strings.HasSuffix(path, "/git/ref/heads/main") && strings.Contains(path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ref": "refs/heads/main", "object": {"sha": "abc123def456"}}`))
		case strings.HasSuffix(path, "/git/refs") && strings.Contains(path, "/repos/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ref": "refs/heads/new-branch", "object": {"sha": "abc123def456"}}`))
		case strings.HasSuffix(path, "/repos/testowner/testrepo"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name": "testrepo", "full_name": "testowner/testrepo", "private": false, "default_branch": "main", "html_url": "https://github.com/testowner/testrepo"}`))
		case strings.HasSuffix(path, "/repos/testowner/testrepo-remote"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name": "testrepo-remote", "full_name": "testowner/testrepo-remote", "private": false, "default_branch": "main"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "Not Found"}`))
		}
	})

	s.server = httptest.NewServer(handler)
}

func (s *GitHubToolTestSuite) TearDownSuite() {
	if s.server != nil {
		s.server.Close()
	}
}

func (s *GitHubToolTestSuite) configs() Configs {
	return Configs{
		"test": {
			Token:    "test-token",
			CloneDir: "/tmp/test-github-clones",
			BaseURL:  s.server.URL + "/",
		},
	}
}
