package kubernetes

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type paginateToken struct {
	PaginateToken string `json:"paginateToken"`
}

// ListParamsPaginate holds pagination state for resource listing.
type ListParamsPaginate struct {
	PageSize      int    `json:"pageSize,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The number of resources to return per page. Default is 50."`
	PaginateToken string `json:"paginateToken,omitempty" jsonschema:"(optional) The token to retrieve the next page of results. This token is returned in the response when there are more results available than can fit in a single page."`
}

// ListParams defines the parameters for listing Kubernetes resources.
type ListParams struct {
	Cluster        string              `json:"cluster" validate:"required" jsonschema:"(required) The cluster to connect to."`
	Kind           string              `json:"kind" validate:"required" jsonschema:"(required) The resource kind in PascalCase singular (e.g. 'Pod', 'Deployment', 'ConfigMap'). Also accepts kubectl shortnames ('po', 'deploy'), and 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are accepted but PascalCase is preferred. Uses server-side discovery so CRDs are supported automatically."`
	Namespace      string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces. Ignored for cluster-scoped kinds."`
	LabelsSelector string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, separated by comma. For example: 'app=nginx,env=prod'."`
	Filter         string              `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each resource JSON output. Keep only the resources that match the pattern. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'app-.*|web-.*'. Invalid regex returns an error."`
	Paginate       *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

const listDescription = `
** General Purpose **
It lists any Kubernetes resource. The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted. Supports core types, CRDs, label selectors, regex filtering, and pagination.

** Output **
Returns a JSON array of objects with curated fields specific to each resource type. For types without dedicated formatters, returns name, namespace, and status.
`

// ListTool is an eino tool for listing Kubernetes resources.
type ListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns matching resources as JSON.
func (t *ListTool) Invoke(ctx context.Context, params *ListParams) (string, error) {
	if params.Paginate != nil && params.Paginate.PageSize == 0 {
		params.Paginate.PageSize = 50
	}

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	resolved, err := t.resolveKind(ctx, params.Cluster, params.Kind)
	if err != nil {
		return "", err
	}

	c, err := t.dynamicClient(params.Cluster)
	if err != nil {
		return "", err
	}

	var ls labels.Selector
	if len(params.LabelsSelector) > 0 {
		ls, err = labels.Parse(params.LabelsSelector)
		if err != nil {
			return "", errors.Wrap(err, "invalid labels selector")
		}
	}

	var namespace string
	if resolved.Scoped {
		namespace = params.Namespace
	}

	listOpts := metav1.ListOptions{}
	if ls != nil {
		listOpts.LabelSelector = ls.String()
	}
	if params.Paginate != nil {
		listOpts.Limit = int64(params.Paginate.PageSize)
		listOpts.Continue = params.Paginate.PaginateToken
	}

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	o, err := c.Resource(resolved.GVR).Namespace(namespace).List(ctx, listOpts)
	if err != nil {
		return "", errors.Wrapf(err, "failed to list %s resources", resolved.GVK.Kind)
	}

	outputs := make([]json.RawMessage, 0, len(o.Items))
	for i := range o.Items {
		item := o.Items[i]
		output := formatListItem(&item)
		if !filter.Match(output, re) {
			continue
		}
		outputs = append(outputs, output)
	}

	accessor, err := apimeta.ListAccessor(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to get list accessor")
	}
	continueToken := accessor.GetContinue()
	if continueToken != "" {
		tokenData := marshal.MustMarshal(paginateToken{PaginateToken: continueToken})
		outputs = append(outputs, json.RawMessage(tokenData))
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewListTool creates a new ListTool.
func NewListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	t := &ListTool{baseTool: base}
	inv, err := utils.InferTool("kubernetes_list",
		fmt.Sprintf("%s\n%s", listDescription, listOutputGuidance),
		t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = inv
	return t, nil
}
