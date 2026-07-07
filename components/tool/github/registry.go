package github

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/components/middleware/safety"
)

// toolConstructor is a function that creates a single GitHub tool from a shared baseTool.
type toolConstructor func(context.Context, *baseTool) (tool.InvokableTool, error)

// readOnlyConstructors lists all read-only GitHub tools.
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newIssueListTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newIssueGetTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRListTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRGetTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newOrgRepoListTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newRepoSearchTool(ctx, b) },
}

// writeConstructors lists all write/destructive GitHub tools.
var writeConstructors = []toolConstructor{
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newRepoCloneTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newBranchCreateTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newReleaseCreateTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newIssueCreateTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newIssueCommentTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRCreateTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRCommentTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRReviewTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRSuggestChangeTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newPRRequestReviewersTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newRepoSettingsUpdateTool(ctx, b) },
	func(ctx context.Context, b *baseTool) (tool.InvokableTool, error) { return newWebhookUpsertTool(ctx, b) },
}

// buildTools creates tools from the given constructors, sharing a single baseTool.
func buildTools(ctx context.Context, configs Configs, constructors []toolConstructor) ([]tool.InvokableTool, error) {
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	tools := make([]tool.InvokableTool, 0, len(constructors))
	for i, fn := range constructors {
		t, err := fn(ctx, base)
		if err != nil {
			return nil, fmt.Errorf("failed to create github tool %d: %w", i, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// NewAllTools creates all GitHub tools (read + write) for the given configurations
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, append(readOnlyConstructors, writeConstructors...))
}

// NewReadOnlyTools creates only the read-only GitHub tools (list + get + search)
// and returns them as a flat slice ready to be registered with an eino ToolsNode.
func NewReadOnlyTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
	return buildTools(ctx, configs, readOnlyConstructors)
}

// WriteToolNames returns the tool names of all GitHub write tools.
func WriteToolNames() []string {
	return []string{
		"github_repo_clone",
		"github_branch_create",
		"github_release_create",
		"github_issue_create",
		"github_issue_comment",
		"github_pr_create",
		"github_pr_comment",
		"github_pr_review",
		"github_pr_suggest_change",
		"github_pr_request_reviewers",
		"github_repo_settings_update",
		"github_webhook_upsert",
	}
}

// ExtractWriteToolNames creates all write tools from the given configs and
// extracts their tool names via Info().
func ExtractWriteToolNames(ctx context.Context, configs Configs) ([]string, error) {
	tools, err := buildTools(ctx, configs, writeConstructors)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		info, infoErr := t.Info(ctx)
		if infoErr != nil {
			return nil, fmt.Errorf("failed to get info for write tool %d: %w", i, infoErr)
		}
		names[i] = info.Name
	}
	return names, nil
}

// NewAllToolsWithSafety creates all GitHub tools (read + write) and returns them
// together with a pre-configured safety middleware.
func NewAllToolsWithSafety(ctx context.Context, configs Configs, safetyCfg *safety.Config) ([]tool.InvokableTool, *safety.Middleware, error) {
	tools, err := NewAllTools(ctx, configs)
	if err != nil {
		return nil, nil, err
	}

	if safetyCfg == nil {
		safetyCfg = &safety.Config{}
	}
	if len(safetyCfg.WriteToolNames) == 0 {
		safetyCfg.WriteToolNames = WriteToolNames()
	}

	mw, err := safety.New(safetyCfg)
	if err != nil {
		return nil, nil, err
	}

	return tools, mw, nil
}

var (
	_ tool.InvokableTool = (*IssueListTool)(nil)
	_ tool.InvokableTool = (*IssueGetTool)(nil)
	_ tool.InvokableTool = (*PRListTool)(nil)
	_ tool.InvokableTool = (*PRGetTool)(nil)
	_ tool.InvokableTool = (*OrgRepoListTool)(nil)
	_ tool.InvokableTool = (*RepoSearchTool)(nil)
	_ tool.InvokableTool = (*RepoCloneTool)(nil)
	_ tool.InvokableTool = (*BranchCreateTool)(nil)
	_ tool.InvokableTool = (*ReleaseCreateTool)(nil)
	_ tool.InvokableTool = (*IssueCreateTool)(nil)
	_ tool.InvokableTool = (*IssueCommentTool)(nil)
	_ tool.InvokableTool = (*PRCreateTool)(nil)
	_ tool.InvokableTool = (*PRCommentTool)(nil)
	_ tool.InvokableTool = (*PRReviewTool)(nil)
	_ tool.InvokableTool = (*PRSuggestChangeTool)(nil)
	_ tool.InvokableTool = (*PRRequestReviewersTool)(nil)
	_ tool.InvokableTool = (*RepoSettingsUpdateTool)(nil)
	_ tool.InvokableTool = (*WebhookUpsertTool)(nil)
)
