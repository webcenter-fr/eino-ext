package kubernetes

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

type paginateToken struct {
	PaginateToken string `json:"paginateToken"`
}

// offsetToken is the self-encoded pagination token used on the filtered path.
type offsetToken struct {
	V      int `json:"v"`
	Offset int `json:"offset"`
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
	APIVersion     string              `json:"apiVersion,omitempty" jsonschema:"(optional) The group/version of the resource, e.g. 'kafka.strimzi.io/v1beta2' or 'v1' for the core group. Required when the kind exists in several API groups."`
	Namespace      string              `json:"namespace,omitempty" jsonschema:"(optional) The namespace to list resources from. If not provided, it will list resources from all namespaces. Ignored for cluster-scoped kinds."`
	LabelsSelector string              `json:"labelsSelector,omitempty" jsonschema:"(optional) The labels selector on string format, separated by comma. For example: 'app=nginx,env=prod'."`
	Filter         string              `json:"filter,omitempty" jsonschema:"(optional) Filter to keep only matching resources. Accepts either: a JSON object selector {\"<dot.path>\":\"<value>\", ...} where all keys must match (AND), [] matches any array element (e.g. 'status.conditions[].reason'), an array value means IN, matching is type-coerced and case-insensitive (500 matches \"500\", true matches \"true\"), and a bare key (no dots) matches any field with that name at any depth; or a Go RE2 regex on the raw resource JSON (RE2 does NOT support lookahead/lookbehind/backreferences). Invalid regex or selector JSON returns an error."`
	Fields         []string            `json:"fields,omitempty" jsonschema:"(optional) List of dot paths to return only those parts of the object (e.g. ['spec.kafka.storage','spec.kafka.resources']). Missing paths are omitted. Use it to keep output small."`
	Paginate       *ListParamsPaginate `json:"paginate,omitempty" jsonschema:"(optional) Pagination parameters."`
}

const listDescription = `
** General Purpose **
It lists any Kubernetes resource. The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted. Supports core types, CRDs, label selectors, structured selector/regex filtering, and pagination.
Pass apiVersion when the kind exists in several API groups (the tool returns an "ambiguous" error listing them).

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

	m, err := filter.CompileMatcher(params.Filter)
	if err != nil {
		return "", err
	}

	resolved, err := t.resolveKind(ctx, params.Cluster, params.Kind, params.APIVersion)
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
	labelSelector := ""
	if ls != nil {
		labelSelector = ls.String()
	}

	var namespace string
	if resolved.Scoped {
		namespace = params.Namespace
	}

	ctx, cancel := withTimeout(ctx, t.getDefaultTimeout(params.Cluster))
	defer cancel()

	if strings.TrimSpace(params.Filter) == "" {
		// No filter: keep the existing server-side pagination semantics.
		listOpts := metav1.ListOptions{LabelSelector: labelSelector}
		if params.Paginate != nil {
			listOpts.Limit = int64(params.Paginate.PageSize)
			listOpts.Continue = params.Paginate.PaginateToken
		}

		o, err := c.Resource(resolved.GVR).Namespace(namespace).List(ctx, listOpts)
		if err != nil {
			return "", errors.Wrapf(err, "failed to list %s resources", resolved.GVK.Kind)
		}

		outputs := t.collectMatches(o, m, params.Fields)

		tok, err := listContinueToken(o)
		if err != nil {
			return "", err
		}
		if tok != "" {
			outputs = append(outputs, json.RawMessage(marshal.MustMarshal(paginateToken{PaginateToken: tok})))
		}
		return marshal.Outputs(outputs)
	}

	// Filtered: iterate all server pages, accumulate only matches, then
	// paginate the filtered set in-memory with a self-encoded offset token.
	pageSize := 0
	if params.Paginate != nil {
		pageSize = params.Paginate.PageSize
	}

	offset := 0
	if params.Paginate != nil && params.Paginate.PaginateToken != "" {
		offset, err = decodeOffsetToken(params.Paginate.PaginateToken)
		if err != nil {
			return "", errors.Wrap(err, "invalid paginate token")
		}
	}

	outputs := make([]json.RawMessage, 0)
	cont := ""
	for {
		listOpts := metav1.ListOptions{LabelSelector: labelSelector, Continue: cont}
		if pageSize > 0 {
			listOpts.Limit = int64(pageSize)
		}
		o, err := c.Resource(resolved.GVR).Namespace(namespace).List(ctx, listOpts)
		if err != nil {
			return "", errors.Wrapf(err, "failed to list %s resources", resolved.GVK.Kind)
		}
		outputs = append(outputs, t.collectMatches(o, m, params.Fields)...)

		cont, err = listContinueToken(o)
		if err != nil {
			return "", err
		}
		if cont == "" {
			break
		}
	}

	if offset < len(outputs) {
		outputs = outputs[offset:]
	} else {
		outputs = []json.RawMessage{}
	}
	if pageSize > 0 && len(outputs) > pageSize {
		outputs = append(outputs[:pageSize],
			json.RawMessage(marshal.MustMarshal(paginateToken{encodeOffsetToken(offset + pageSize)})))
	}
	return marshal.Outputs(outputs)
}

// listContinueToken returns the server-side continue token of a list result.
func listContinueToken(o *unstructured.UnstructuredList) (string, error) {
	accessor, err := apimeta.ListAccessor(o)
	if err != nil {
		return "", errors.Wrap(err, "failed to get list accessor")
	}
	return accessor.GetContinue(), nil
}

// collectMatches filters the raw objects of a list against m, then projects and
// formats the survivors. metadata.managedFields is removed in place before
// matching so regex scans stay small.
func (t *ListTool) collectMatches(o *unstructured.UnstructuredList, m filter.Matcher, fields []string) []json.RawMessage {
	outputs := make([]json.RawMessage, 0, len(o.Items))
	for i := range o.Items {
		item := &o.Items[i]
		unstructured.RemoveNestedField(item.Object, "metadata", "managedFields")
		if !m.MatchObject(item.Object) {
			continue
		}
		outputs = append(outputs, formatListItem(projectForOutput(item, fields)))
	}
	return outputs
}

// projectForOutput applies the fields projection to item, always keeping
// apiVersion, kind, metadata.name and metadata.namespace so curated formatters
// still match. It returns item unchanged when no fields are requested.
func projectForOutput(item *unstructured.Unstructured, fields []string) *unstructured.Unstructured {
	if len(fields) == 0 {
		return item
	}

	projected := projectFields(item.Object, fields)
	projected["apiVersion"] = item.GetAPIVersion()
	projected["kind"] = item.GetKind()
	if meta, ok := item.Object["metadata"].(map[string]any); ok {
		pm, _ := projected["metadata"].(map[string]any)
		if pm == nil {
			pm = map[string]any{}
			projected["metadata"] = pm
		}
		if name, ok := meta["name"]; ok {
			pm["name"] = name
		}
		if ns, ok := meta["namespace"]; ok {
			pm["namespace"] = ns
		}
	}
	return &unstructured.Unstructured{Object: projected}
}

// encodeOffsetToken encodes an in-memory offset as an opaque pagination token.
func encodeOffsetToken(offset int) string {
	return base64.StdEncoding.EncodeToString(marshal.MustMarshal(offsetToken{V: 1, Offset: offset}))
}

// decodeOffsetToken decodes a token produced by encodeOffsetToken.
func decodeOffsetToken(token string) (int, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	var t offsetToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return 0, err
	}
	if t.V != 1 {
		return 0, errors.Errorf("unsupported token version %d", t.V)
	}
	if t.Offset < 0 {
		return 0, errors.Errorf("invalid offset %d", t.Offset)
	}
	return t.Offset, nil
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
