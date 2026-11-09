package github

import (
	"bytes"
	"context"
	"os"
	"sort"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitdiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/goccy/go-json"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
)

const diffDescription = `
** General Purpose **
It returns the staged and unstaged diff of a previously cloned GitHub repository
relative to HEAD.

** Output **
It returns {diff, truncated}. The diff is bounded by MaxBytes (default 100000).

** Important **
- The repository must already be cloned under <CloneDir>/<session>/<owner>/<repo>.
- The diff combines staged and unstaged changes (equivalent to git diff HEAD).
- Binary files and files larger than 10MB are reported as a note, not diffed.
- Files are processed in lexical order for stable output.
`

// DiffParams defines the parameters for reading a worktree diff.
type DiffParams struct {
	Instance string `json:"instance"   validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner    string `json:"owner"      validate:"required" jsonschema:"(required) Repository owner."`
	Repo     string `json:"repo"       validate:"required" jsonschema:"(required) Repository name."`
	MaxBytes int    `json:"maxBytes,omitempty" validate:"omitempty,gte=0" jsonschema:"(optional) Maximum diff size in bytes. Defaults to 100000."`
}

// DiffOutput is the bounded worktree diff.
type DiffOutput struct {
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

// DiffTool is an eino tool for reading a worktree diff.
type DiffTool struct {
	*baseTool
	tool.InvokableTool
}

const defaultDiffMaxBytes = 100_000

// Invoke returns the staged and unstaged diff relative to HEAD.
func (t *DiffTool) Invoke(ctx context.Context, params *DiffParams) (string, error) {
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
	status, err := wt.Status()
	if err != nil {
		return "", errors.Wrap(err, "failed to read worktree status")
	}

	paths := make([]string, 0, len(status))
	for path := range status {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var headTree *object.Tree
	if head, headErr := repo.Head(); headErr == nil {
		if commit, commitErr := repo.CommitObject(head.Hash()); commitErr == nil {
			if tree, treeErr := commit.Tree(); treeErr == nil {
				headTree = tree
			}
		}
	}

	maxBytes := params.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultDiffMaxBytes
	}

	// Bound the buffer as it is built so a repository with many large changed
	// files cannot exhaust memory before the final truncation (CWE-400). Each
	// individual file is already capped by diffContents, so the buffer stays
	// within maxBytes plus one file's diff.
	var buf bytes.Buffer
	truncated := false
	for _, path := range paths {
		if buf.Len() >= maxBytes {
			truncated = true
			break
		}
		oldContent, newContent, note := diffContents(clonePath_, headTree, path)
		if note != "" {
			buf.WriteString(note)
			buf.WriteString("\n")
			continue
		}
		if oldContent == newContent {
			continue
		}
		buf.WriteString("diff --git a/")
		buf.WriteString(path)
		buf.WriteString(" b/")
		buf.WriteString(path)
		buf.WriteString("\n--- a/")
		buf.WriteString(path)
		buf.WriteString("\n+++ b/")
		buf.WriteString(path)
		buf.WriteString("\n")
		for _, d := range gitdiff.Do(oldContent, newContent) {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				writeDiffLines(&buf, " ", d.Text)
			case diffmatchpatch.DiffDelete:
				writeDiffLines(&buf, "-", d.Text)
			case diffmatchpatch.DiffInsert:
				writeDiffLines(&buf, "+", d.Text)
			}
		}
	}

	diffText := buf.String()
	if len(diffText) > maxBytes {
		truncated = true
	}
	diffText = strutil.Truncate(diffText, maxBytes, "\n... (truncated)")

	output := &DiffOutput{Diff: diffText, Truncated: truncated}
	result, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(result), nil
}

func diffContents(clonePath_ string, headTree *object.Tree, path string) (oldContent, newContent, note string) {
	if headTree != nil {
		if file, err := headTree.File(path); err == nil {
			// Check the blob size before reading so a large committed file is
			// never fully loaded into memory (CWE-400).
			if file.Size > fileutil.DefaultMaxSearchFileBytes {
				return "", "", "[skipped large file: " + path + "]"
			}
			if content, contentErr := file.Contents(); contentErr == nil {
				oldContent = content
			}
		}
	}

	fullPath, err := fileutil.ValidateRelativePath(clonePath_, path)
	if err != nil {
		return "", "", "[skipped unsafe path: " + path + "]"
	}
	if fi, statErr := os.Lstat(fullPath); statErr == nil {
		// Check the working-tree file size before reading so a large modified
		// or untracked file is never fully loaded into memory (CWE-400).
		if fi.Size() > fileutil.DefaultMaxSearchFileBytes {
			return "", "", "[skipped large file: " + path + "]"
		}
		safePath, safeErr := fileutil.ResolveSymlinkSafe(clonePath_, fullPath, false)
		if safeErr != nil {
			return "", "", "[skipped unsafe path: " + path + "]"
		}
		if data, readErr := os.ReadFile(safePath); readErr == nil {
			newContent = string(data)
		}
	}

	if len(oldContent) > fileutil.DefaultMaxSearchFileBytes || len(newContent) > fileutil.DefaultMaxSearchFileBytes {
		return "", "", "[skipped large file: " + path + "]"
	}
	if fileutil.IsBinary([]byte(oldContent)) || fileutil.IsBinary([]byte(newContent)) {
		return "", "", "[binary file differs: " + path + "]"
	}
	return oldContent, newContent, ""
}

func writeDiffLines(buf *bytes.Buffer, prefix, text string) {
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		buf.WriteString(prefix)
		buf.WriteString(line)
		buf.WriteString("\n")
	}
}

// NewDiffTool creates a new DiffTool.
func NewDiffTool(ctx context.Context, configs Configs) (*DiffTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newDiffTool(ctx, base)
}

func newDiffTool(ctx context.Context, base *baseTool) (*DiffTool, error) {
	diffTool := &DiffTool{baseTool: base}
	t, err := utils.InferTool("github_diff", diffDescription, diffTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	diffTool.InvokableTool = t
	return diffTool, nil
}
