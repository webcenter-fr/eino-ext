package github

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const prGetDescription = `
** General Purpose **
It gets the details of a specific GitHub pull request.

** Output **
It returns a JSON object representing the pull request with full details.
`

type PRGetParams struct {
	Instance            string   `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner               string   `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo                string   `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	Number              int      `json:"number" validate:"required" jsonschema:"(required) PR number."`
	ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=body labels assignees" jsonschema:"(optional) Fields to exclude: 'body', 'labels', 'assignees'."`
}

type PRGetOutput struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	State      string   `json:"state"`
	Author     string   `json:"author"`
	Body       string   `json:"body,omitempty"`
	BaseBranch string   `json:"baseBranch"`
	HeadBranch string   `json:"headBranch"`
	Labels     []string `json:"labels,omitempty"`
	Assignees  []string `json:"assignees,omitempty"`
	Mergeable  *bool    `json:"mergeable"`
	Draft      bool     `json:"draft"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
	HTMLURL    string   `json:"htmlURL"`
}

type PRGetTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *PRGetTool) Invoke(ctx context.Context, params *PRGetParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	pr, _, err := c.PullRequests.Get(ctx, params.Owner, params.Repo, params.Number)
	if err != nil {
		return "", errors.Wrap(err, "failed to get pull request")
	}

	author := ""
	if pr.User != nil {
		author = pr.User.GetLogin()
	}

	headBranch := ""
	if pr.Head != nil {
		headBranch = pr.Head.GetRef()
	}
	baseBranch := ""
	if pr.Base != nil {
		baseBranch = pr.Base.GetRef()
	}

	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}

	assignees := make([]string, 0, len(pr.Assignees))
	for _, a := range pr.Assignees {
		assignees = append(assignees, a.GetLogin())
	}

	output := &PRGetOutput{
		Number:     pr.GetNumber(),
		Title:      pr.GetTitle(),
		State:      pr.GetState(),
		Author:     author,
		Body:       pr.GetBody(),
		BaseBranch: baseBranch,
		HeadBranch: headBranch,
		Labels:     labels,
		Assignees:  assignees,
		Mergeable:  pr.Mergeable,
		Draft:      pr.GetDraft(),
		CreatedAt:  pr.GetCreatedAt().String(),
		UpdatedAt:  pr.GetUpdatedAt().String(),
		HTMLURL:    pr.GetHTMLURL(),
	}

	if err := applyExcludes(params.ExcludeFieldsOutput, map[string]func(){
		"body":      func() { output.Body = "" },
		"labels":    func() { output.Labels = nil },
		"assignees": func() { output.Assignees = nil },
	}); err != nil {
		return "", err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewPRGetTool(ctx context.Context, configs Configs) (*PRGetTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newPRGetTool(ctx, base)
}

func newPRGetTool(ctx context.Context, base *baseTool) (*PRGetTool, error) {
	getTool := &PRGetTool{baseTool: base}
	t, err := utils.InferTool("github_pr_get", fmt.Sprintf("%s\n%s", prGetDescription, describeOutputGuidance), getTool.Invoke)
	if err != nil {
		return nil, err
	}
	getTool.InvokableTool = t

	return getTool, nil
}
