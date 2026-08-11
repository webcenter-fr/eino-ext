package github

import (
	"context"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/goccy/go-json"
)

func (s *GitHubToolTestSuite) setupClone() (cloneDir string, cleanup func()) {
	dir, err := os.MkdirTemp("", "eino-ext-file-test")
	s.NoError(err)

	repoPath := filepath.Join(dir, "testowner", "testrepo")
	err = os.MkdirAll(repoPath, 0o755)
	s.NoError(err)

	repo, err := git.PlainInit(repoPath, false)
	s.NoError(err)

	wt, err := repo.Worktree()
	s.NoError(err)

	err = os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("line1\nline2\nline3\n"), 0o644)
	s.NoError(err)
	err = os.MkdirAll(filepath.Join(repoPath, "sub"), 0o755)
	s.NoError(err)
	err = os.WriteFile(filepath.Join(repoPath, "sub", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	s.NoError(err)
	err = os.WriteFile(filepath.Join(repoPath, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644)
	s.NoError(err)

	_, err = wt.Add("README.md")
	s.NoError(err)
	_, err = wt.Add("sub/main.go")
	s.NoError(err)
	_, err = wt.Add("binary.bin")
	s.NoError(err)

	_, err = wt.Commit("initial", &git.CommitOptions{Author: commitIdentity})
	s.NoError(err)

	return dir, func() { os.RemoveAll(dir) }
}

func (s *GitHubToolTestSuite) fileConfigs(cloneDir string) Configs {
	return Configs{
		"test": {
			Token:    "test-token",
			CloneDir: cloneDir,
			BaseURL:  s.server.URL + "/",
		},
	}
}

// ---- github_file_read ----

func (s *GitHubToolTestSuite) TestFileReadFull() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md"}`)
	s.NoError(err)

	var output FileReadOutput
	err = json.Unmarshal([]byte(result), &output)
	s.NoError(err)
	s.Equal("README.md", output.Path)
	s.Equal("line1\nline2\nline3\n", output.Content)
	s.Equal(18, output.Bytes)
	s.False(output.Truncated)
}

func (s *GitHubToolTestSuite) TestFileReadLineRange() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "startLine": 2, "endLine": 3}`)
	s.NoError(err)

	var output FileReadOutput
	err = json.Unmarshal([]byte(result), &output)
	s.NoError(err)
	s.Equal("line2\nline3", output.Content)
}

func (s *GitHubToolTestSuite) TestFileReadStartLineOnly() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "startLine": 2}`)
	s.NoError(err)

	var output FileReadOutput
	err = json.Unmarshal([]byte(result), &output)
	s.NoError(err)
	s.Equal("line2\nline3\n", output.Content)
}

func (s *GitHubToolTestSuite) TestFileReadNotFound() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "nonexistent.txt"}`)
	s.Error(err)
	s.Contains(err.Error(), "not found")
}

func (s *GitHubToolTestSuite) TestFileReadPathTraversal() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "../../../etc/passwd"}`)
	s.Error(err)
	s.Contains(err.Error(), "escapes clone directory")
}

func (s *GitHubToolTestSuite) TestFileReadBinary() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "binary.bin"}`)
	s.Error(err)
	s.Contains(err.Error(), "binary")
}

func (s *GitHubToolTestSuite) TestFileReadNotCloned() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	configs := Configs{
		"test": {
			Token:    "test-token",
			CloneDir: filepath.Join(cloneDir, "nonexistent"),
			BaseURL:  s.server.URL + "/",
		},
	}

	tool, err := NewFileReadTool(ctx, configs)
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md"}`)
	s.Error(err)
	s.Contains(err.Error(), "github_repo_clone")
}

func (s *GitHubToolTestSuite) TestFileReadDotGit() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": ".git/HEAD"}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestFileReadDotGitBypass() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// "./.git/HEAD" must be rejected — cleaning the path yields ".git/HEAD".
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "./.git/HEAD"}`)
	s.Error(err)
	s.Contains(err.Error(), ".git")

	// "subdir/../.git/HEAD" must also be rejected after cleaning.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "subdir/../.git/HEAD"}`)
	s.Error(err)
	s.Contains(err.Error(), ".git")
}

func (s *GitHubToolTestSuite) TestFileReadSymlinkTraversal() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	// Create a file outside the clone directory.
	outsideDir, err := os.MkdirTemp("", "eino-ext-outside")
	s.NoError(err)
	defer os.RemoveAll(outsideDir)
	err = os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o644)
	s.NoError(err)

	// Plant a symlink inside the clone that points outside.
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	err = os.Symlink(outsideDir, filepath.Join(repoPath, "link"))
	s.NoError(err)

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Attempt to read through the symlink — must be rejected.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "link/secret.txt"}`)
	s.Error(err)
	s.Contains(err.Error(), "symlink")
}

func (s *GitHubToolTestSuite) TestFileReadEmptyFile() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	err := os.WriteFile(filepath.Join(repoPath, "empty.txt"), []byte{}, 0o644)
	s.NoError(err)

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "empty.txt"}`)
	s.NoError(err)

	var output FileReadOutput
	err = json.Unmarshal([]byte(result), &output)
	s.NoError(err)
	s.Equal("", output.Content)
	s.Equal(0, output.Bytes)
}

func (s *GitHubToolTestSuite) TestFileReadLargeFile() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	largeContent := make([]byte, maxFileReadBytes+100)
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	largeContent[len(largeContent)-1] = '\n'
	err := os.WriteFile(filepath.Join(repoPath, "large.txt"), largeContent, 0o644)
	s.NoError(err)

	tool, err := NewFileReadTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "large.txt"}`)
	s.NoError(err)

	var output FileReadOutput
	err = json.Unmarshal([]byte(result), &output)
	s.NoError(err)
	s.True(output.Truncated)
	s.Contains(output.Note, "truncated")
}

// ---- github_file_search ----

func (s *GitHubToolTestSuite) TestFileSearchBasic() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 3)
	for _, o := range outputs {
		s.Equal("README.md", o.Path)
	}
}

func (s *GitHubToolTestSuite) TestFileSearchRegex() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "func .*\\{"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal("sub/main.go", outputs[0].Path)
}

func (s *GitHubToolTestSuite) TestFileSearchPathPrefix() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "package|func", "pathPrefix": "sub"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.NotEmpty(outputs)
	for _, o := range outputs {
		s.Contains(o.Path, "sub/")
	}
}

func (s *GitHubToolTestSuite) TestFileSearchMaxResults() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line", "maxResults": 1}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
}

func (s *GitHubToolTestSuite) TestFileSearchInvalidRegex() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "(invalid"}`)
	s.Error(err)
	s.Contains(err.Error(), "invalid search pattern")
}

func (s *GitHubToolTestSuite) TestFileSearchNoMatches() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "zzz"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 0)
}

func (s *GitHubToolTestSuite) TestFileSearchSkipsBinary() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": ".*"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	for _, o := range outputs {
		s.NotEqual("binary.bin", o.Path)
	}
}

func (s *GitHubToolTestSuite) TestFileSearchSkipsGit() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileSearchTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": ".*"}`)
	s.NoError(err)

	var outputs []FileSearchOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	for _, o := range outputs {
		s.NotContains(o.Path, ".git/")
	}
}

func (s *GitHubToolTestSuite) TestFileSearchNotCloned() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	configs := Configs{
		"test": {
			Token:    "test-token",
			CloneDir: filepath.Join(cloneDir, "nonexistent"),
			BaseURL:  s.server.URL + "/",
		},
	}

	tool, err := NewFileSearchTool(ctx, configs)
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "pattern": "line"}`)
	s.Error(err)
	s.Contains(err.Error(), "github_repo_clone")
}

// ---- github_file_list ----

func (s *GitHubToolTestSuite) TestFileListRoot() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.NotEmpty(outputs)

	names := make(map[string]bool)
	for _, o := range outputs {
		names[o.Name] = true
	}
	s.True(names["README.md"], "README.md should be present")
	s.True(names["sub"], "sub should be present")
	s.True(names["binary.bin"], "binary.bin should be present")
}

func (s *GitHubToolTestSuite) TestFileListSubPath() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "subPath": "sub"}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
	s.Equal("main.go", outputs[0].Name)
}

func (s *GitHubToolTestSuite) TestFileListMaxDepth1() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 1}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	for _, o := range outputs {
		s.NotEqual("main.go", o.Name, "main.go in sub/ should not be present at depth 1")
	}
}

func (s *GitHubToolTestSuite) TestFileListMaxDepth2() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 2}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)

	hasMainGo := false
	for _, o := range outputs {
		if o.Name == "main.go" {
			hasMainGo = true
			break
		}
	}
	s.True(hasMainGo, "main.go should be included at depth 2")
}

func (s *GitHubToolTestSuite) TestFileListMaxResults() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxResults": 1}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	s.Len(outputs, 1)
}

func (s *GitHubToolTestSuite) TestFileListSkipsGit() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "maxDepth": 5}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	for _, o := range outputs {
		s.NotEqual(".git", o.Name)
		s.NotContains(o.Path, ".git/")
	}
}

func (s *GitHubToolTestSuite) TestFileListSkipsSymlinks() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	err := os.Symlink(filepath.Join(repoPath, "README.md"), filepath.Join(repoPath, "link.md"))
	s.NoError(err)

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	s.NoError(err)

	var outputs []FileListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	s.NoError(err)
	for _, o := range outputs {
		s.NotEqual("link.md", o.Name, "symlink should not appear")
	}
}

func (s *GitHubToolTestSuite) TestFileListNotCloned() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	configs := Configs{
		"test": {
			Token:    "test-token",
			CloneDir: filepath.Join(cloneDir, "nonexistent"),
			BaseURL:  s.server.URL + "/",
		},
	}

	tool, err := NewFileListTool(ctx, configs)
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo"}`)
	s.Error(err)
	s.Contains(err.Error(), "github_repo_clone")
}

func (s *GitHubToolTestSuite) TestFileListPathTraversal() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileListTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "subPath": "../"}`)
	s.Error(err)
	s.Contains(err.Error(), "escapes clone directory")
}

// ---- github_file_write ----

func (s *GitHubToolTestSuite) TestFileWriteDryRun() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.Info(ctx)
	s.NoError(err)

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "newfile.txt", "content": "hello", "branch": "master", "commitMessage": "commit msg", "dryRun": true}`)
	s.NoError(err)
	s.Contains(result, `"dryRun":true`)
	s.Contains(result, `"wouldWrite"`)
	s.Contains(result, `newfile.txt`)

	// Verify file was not actually written
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	_, err = os.Stat(filepath.Join(repoPath, "newfile.txt"))
	s.True(os.IsNotExist(err))
}

func (s *GitHubToolTestSuite) TestFileWriteConfirmed() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Push will fail against the mock server, but the commit should succeed locally.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "newfile.txt", "content": "hello world", "branch": "master", "commitMessage": "add newfile", "confirmed": true}`)
	// Push fails, but commit succeeded; verify file was written locally.
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	data, readErr := os.ReadFile(filepath.Join(repoPath, "newfile.txt"))
	s.NoError(readErr)
	s.Equal("hello world", string(data))
	_ = err // push error is expected
}

func (s *GitHubToolTestSuite) TestFileWriteNotConfirmed() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "newfile.txt", "content": "hello", "branch": "master", "commitMessage": "commit msg"}`)
	s.Error(err)
	s.Contains(err.Error(), "Confirmed")
}

func (s *GitHubToolTestSuite) TestFileWriteNewBranch() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Push will fail against mock server, but branch creation and commit should succeed locally.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "feature.txt", "content": "feature content", "branch": "new-feature", "baseBranch": "master", "commitMessage": "add feature", "confirmed": true}`)
	// Verify file was written locally (commit succeeded before push failed)
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	data, readErr := os.ReadFile(filepath.Join(repoPath, "feature.txt"))
	s.NoError(readErr)
	s.Equal("feature content", string(data))
	_ = err
}

func (s *GitHubToolTestSuite) TestFileWriteOverwrite() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Push will fail against mock server, but overwrite should succeed locally.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "README.md", "content": "overwritten content", "branch": "master", "commitMessage": "update readme", "confirmed": true}`)
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	data, readErr := os.ReadFile(filepath.Join(repoPath, "README.md"))
	s.NoError(readErr)
	s.Equal("overwritten content", string(data))
	_ = err
}

func (s *GitHubToolTestSuite) TestFileWritePathTraversal() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "../../evil.txt", "content": "evil", "branch": "master", "commitMessage": "test", "confirmed": true}`)
	s.Error(err)
	s.Contains(err.Error(), "escapes clone directory")
}

func (s *GitHubToolTestSuite) TestFileWriteSymlinkTraversal() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	// Create a directory outside the clone.
	outsideDir, err := os.MkdirTemp("", "eino-ext-outside")
	s.NoError(err)
	defer os.RemoveAll(outsideDir)

	// Plant a symlink inside the clone that points outside.
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	err = os.Symlink(outsideDir, filepath.Join(repoPath, "link"))
	s.NoError(err)

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Attempt to write through the symlink — must be rejected.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "link/evil.txt", "content": "evil", "branch": "master", "commitMessage": "test", "confirmed": true}`)
	s.Error(err)
	s.Contains(err.Error(), "symlink")

	// Verify the file was NOT written outside the clone.
	_, statErr := os.Stat(filepath.Join(outsideDir, "evil.txt"))
	s.True(os.IsNotExist(statErr), "file must not be written outside clone via symlink")
}

func (s *GitHubToolTestSuite) TestFileWriteDotGitBypass() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "./.git/evil.txt", "content": "evil", "branch": "master", "commitMessage": "test", "confirmed": true}`)
	s.Error(err)
	s.Contains(err.Error(), ".git")
}

func (s *GitHubToolTestSuite) TestFileWriteNotCloned() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	configs := Configs{
		"test": {
			Token:    "test-token",
			CloneDir: filepath.Join(cloneDir, "nonexistent"),
			BaseURL:  s.server.URL + "/",
		},
	}

	tool, err := NewFileWriteTool(ctx, configs)
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "test.txt", "content": "test", "branch": "master", "commitMessage": "test", "confirmed": true}`)
	s.Error(err)
}

func (s *GitHubToolTestSuite) TestFileWriteCreatesParentDirs() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	// Push will fail against mock server, but dirs should be created locally.
	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "deep/nested/file.txt", "content": "nested content", "branch": "master", "commitMessage": "create nested file", "confirmed": true}`)
	repoPath := filepath.Join(cloneDir, "testowner", "testrepo")
	data, readErr := os.ReadFile(filepath.Join(repoPath, "deep", "nested", "file.txt"))
	s.NoError(readErr)
	s.Equal("nested content", string(data))
	_ = err
}

func (s *GitHubToolTestSuite) TestFileWritePushRejection() {
	ctx := context.Background()
	cloneDir, cleanup := s.setupClone()
	defer cleanup()

	// Use a mock server URL that points to a non-git HTTP server; the push
	// will fail with a transport/connection error and the token should be
	// redacted in the error message.
	tool, err := NewFileWriteTool(ctx, s.fileConfigs(cloneDir))
	s.NoError(err)

	_, err = tool.InvokableRun(ctx, `{"instance": "test", "owner": "testowner", "repo": "testrepo", "path": "push-test.txt", "content": "push test", "branch": "master", "commitMessage": "test push rejection", "confirmed": true}`)
	s.Error(err, "expected push error with mock server")
	s.NotContains(err.Error(), "test-token", "token should be redacted")
}
