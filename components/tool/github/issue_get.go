package github

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const issueGetDescription = `
** General Purpose **
It gets the details of a specific GitHub issue.

** Output **
It returns a JSON object representing the issue with full details.
`

// IssueGetParams defines the parameters for getting a GitHub issue.
type IssueGetParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner               string   `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo                string   `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number              int      `json:"number" validate:"required" jsonschema:"(required) Issue number."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=body labels assignees milestones" jsonschema:"(optional) Fields to exclude: 'body', 'labels', 'assignees', 'milestones'."`
}

// IssueGetOutput is the structured output for an issue get.
type IssueGetOutput struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Author    string   `json:"author"`
	Body      string   `json:"body,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Milestone string   `json:"milestone,omitempty"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	HTMLURL   string   `json:"htmlURL"`
}

// IssueGetTool is an eino tool for getting GitHub issues.
type IssueGetTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke fetches a GitHub issue and returns the result.
func (t *IssueGetTool) Invoke(ctx context.Context, params *IssueGetParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	issue, _, err := c.Issues.Get(ctx, params.Owner, params.Repo, params.Number)
	if err != nil {
		return "", errors.Wrap(err, "failed to get issue")
	}

	author := ""
	if issue.User != nil {
		author = issue.User.GetLogin()
	}

	labels := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		labels = append(labels, l.GetName())
	}

	assignees := make([]string, 0, len(issue.Assignees))
	for _, a := range issue.Assignees {
		assignees = append(assignees, a.GetLogin())
	}

	milestone := ""
	if issue.Milestone != nil {
		milestone = issue.Milestone.GetTitle()
	}

	output := &IssueGetOutput{
		Number:    issue.GetNumber(),
		Title:     issue.GetTitle(),
		State:     issue.GetState(),
		Author:    author,
		Body:      issue.GetBody(),
		Labels:    labels,
		Assignees: assignees,
		Milestone: milestone,
		CreatedAt: issue.GetCreatedAt().String(),
		UpdatedAt: issue.GetUpdatedAt().String(),
		HTMLURL:   issue.GetHTMLURL(),
	}

	if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
		"body":       func() { output.Body = "" },
		"labels":     func() { output.Labels = nil },
		"assignees":  func() { output.Assignees = nil },
		"milestones": func() { output.Milestone = "" },
	}); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewIssueGetTool creates a new IssueGetTool.
func NewIssueGetTool(ctx context.Context, configs Configs) (*IssueGetTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newIssueGetTool(ctx, base)
}

func newIssueGetTool(ctx context.Context, base *baseTool) (*IssueGetTool, error) {
	getTool := &IssueGetTool{baseTool: base}
	t, err := utils.InferTool("github_issue_get", fmt.Sprintf("%s\n%s", issueGetDescription, describeOutputGuidance), getTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	getTool.InvokableTool = t

	return getTool, nil
}
