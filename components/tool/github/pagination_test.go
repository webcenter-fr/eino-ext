package github

import (
	"context"

	"github.com/goccy/go-json"
)

func (s *GitHubToolTestSuite) TestFileSearchPagination() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	page := func(args string) FileSearchResult {
		result, err := tool.InvokableRun(ctx, args)
		s.NoError(err)
		var output FileSearchResult
		s.NoError(json.Unmarshal([]byte(result), &output))
		return output
	}

	first := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line", "maxResults": 2}`)
	s.Len(first.Results, 2)
	s.True(first.HasMore)
	s.Equal(2, first.NextOffset)

	second := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line", "maxResults": 2, "offset": 2}`)
	s.Len(second.Results, 1)
	s.False(second.HasMore)
	s.Equal(3, second.NextOffset)

	// Concatenating pages yields the full result set in stable order.
	all := append(first.Results, second.Results...)
	s.Len(all, 3)
	for i, match := range all {
		s.Equal("README.md", match.Path)
		s.Equal(i+1, match.Line)
	}

	// Offset beyond the end returns an empty page.
	beyond := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line", "maxResults": 2, "offset": 10}`)
	s.Empty(beyond.Results)
	s.False(beyond.HasMore)
	s.Equal(10, beyond.NextOffset)
}

func (s *GitHubToolTestSuite) TestFileSearchPaginationDeterministic() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	args := `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line", "maxResults": 10}`
	first := ""
	second := ""
	for i := 0; i < 2; i++ {
		result, err := tool.InvokableRun(ctx, args)
		s.NoError(err)
		if i == 0 {
			first = result
		} else {
			second = result
		}
	}
	s.Equal(first, second, "walk order must be deterministic")
}

func (s *GitHubToolTestSuite) TestFileListPagination() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	page := func(args string) FileListResult {
		result, err := tool.InvokableRun(ctx, args)
		s.NoError(err)
		var output FileListResult
		s.NoError(json.Unmarshal([]byte(result), &output))
		return output
	}

	first := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 1, "maxResults": 2}`)
	s.Len(first.Results, 2)
	s.True(first.HasMore)
	s.Equal(2, first.NextOffset)

	second := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 1, "maxResults": 2, "offset": 2}`)
	s.Len(second.Results, 1)
	s.False(second.HasMore)
	s.Equal(3, second.NextOffset)

	all := append(first.Results, second.Results...)
	s.Len(all, 3)
	paths := make([]string, 0, len(all))
	for _, entry := range all {
		paths = append(paths, entry.Path)
	}
	s.Equal([]string{"README.md", "binary.bin", "sub"}, paths)

	beyond := page(`{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 1, "maxResults": 2, "offset": 10}`)
	s.Empty(beyond.Results)
	s.False(beyond.HasMore)
	s.Equal(10, beyond.NextOffset)
}

func (s *GitHubToolTestSuite) TestFileListPaginationDeterministic() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	args := `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 2, "maxResults": 100}`
	first := ""
	second := ""
	for i := 0; i < 2; i++ {
		result, err := tool.InvokableRun(ctx, args)
		s.NoError(err)
		if i == 0 {
			first = result
		} else {
			second = result
		}
	}
	s.Equal(first, second, "walk order must be deterministic")
}

func (s *GitHubToolTestSuite) TestFileListUnlimitedDepth() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 0, "maxResults": 100}`)
	s.NoError(err)

	var output FileListResult
	s.NoError(json.Unmarshal([]byte(result), &output))

	hasMainGo := false
	for _, entry := range output.Results {
		if entry.Name == "main.go" {
			hasMainGo = true
			break
		}
	}
	s.True(hasMainGo, "maxDepth=0 must mean unlimited depth")
}
