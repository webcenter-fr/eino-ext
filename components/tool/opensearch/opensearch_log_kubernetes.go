package opensearch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/disaster37/opensearch/v3/config"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	opensearchv4api "github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/sirupsen/logrus"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const opensearchLogKubernetesDescription = `
** General Purpose **
It retrieves logs from Opensearch about pods in a Kubernetes cluster.
It is useful to get logs when a pod no longer exists in the Kubernetes cluster, but logs still exist in Opensearch.
It is useful to filter logs with lucene query syntax.

** Parameters **
You need to provide podName and/or containerName.
Never put on luceneQuery the cluster, namespace, podName or containerName, because they are already filtered by dedicated parameters, and putting them in luceneQuery can cause issues with query performance.

** Output **
It returns the logs in string format.
`

// OpensearchLogKubernetesParams defines the parameters for the OpensearchLogKubernetes function.
type OpensearchLogKubernetesParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The Kubernetes cluster to retrieve logs from."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pods to retrieve logs from."`
	PodName       string `json:"podName,omitempty" jsonschema:"(optional) The name of the pod to retrieve logs from."`
	ContainerName string `json:"containerName,omitempty" jsonschema:"(optional) The name of the container to retrieve logs from."`
	From          string `json:"from,omitempty" jsonschema:"(optional) The start time to retrieve logs from. Relative format like 'now-1h' or absolute format in RFC3339 format. Default to 'now-24h'."`
	To            string `json:"to,omitempty" jsonschema:"(optional) The end time to retrieve logs to. Relative format like 'now' or absolute format in RFC3339 format. Default to 'now'."`
	LuceneQuery   string `json:"luceneQuery,omitempty" jsonschema:"(optional) The Lucene query to filter logs."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
}

// OpensearchLogKubernetesTool is a tool that retrieves logs from Opensearch about pods in a Kubernetes cluster. It implements the InvokableTool interface.
type OpensearchLogKubernetesTool struct {
	tool.InvokableTool
	tool.StreamableTool
	client opensearchv4.Client
}

// applyDefaults applies the default values to the optional parameters when they are not set.
func (params *OpensearchLogKubernetesParams) applyDefaults() {
	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	if params.From == "" {
		params.From = "now-24h"
	}
	if params.To == "" {
		params.To = "now"
	}
}

// runSearch validates parameters, builds the OpenSearch query, executes the search,
// and returns the raw search result. It is shared by Invoke and InvokeAsStream.
func (t *OpensearchLogKubernetesTool) runSearch(ctx context.Context, params *OpensearchLogKubernetesParams) (*querydsl.SearchResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultOpensearchTimeout)
		defer cancel()
	}

	params.applyDefaults()
	if err := validate.Struct(params); err != nil {
		return nil, err
	}
	if params.PodName == "" && params.ContainerName == "" {
		return nil, errors.New("at least one of podName or containerName must be provided")
	}

	boolQuery := t.buildQuery(params)

	searchReq := querydsl.NewSearchRequest().
		Query(boolQuery).
		Sort("@timestamp", false).
		Size(int(params.MaxLines)).
		DocValueFields("event.original").
		FetchSource(false).
		TrackTotalHits(true)

	searchRes, err := t.client.Search().Search(ctx, &opensearchv4api.SearchRequest{
		Indices: []string{"*"},
		Body:    searchReq,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to search logs in Opensearch")
	}

	return searchRes, nil
}

// Invoke executes the OpensearchLogKubernetesTool with the given parameters. It validates the parameters, builds an OpenSearch query, executes the search, and returns the logs as a single string.
func (t *OpensearchLogKubernetesTool) Invoke(ctx context.Context, params *OpensearchLogKubernetesParams) (result string, err error) {
	searchRes, err := t.runSearch(ctx, params)
	if err != nil {
		return "", err
	}

	if searchRes == nil || searchRes.Hits == nil || len(searchRes.Hits.Hits) == 0 {
		logrus.Debug("No result found")
		return "No result found", nil
	}

	logs := make([]string, 0, len(searchRes.Hits.Hits))

	var totalHits int64
	if searchRes.Hits.TotalHits != nil {
		totalHits = searchRes.Hits.TotalHits.Value
	}

	logrus.Debugf("Total log available %d logs", totalHits)
	logrus.Debugf("Retrieved %d logs", len(searchRes.Hits.Hits))
	for _, hit := range searchRes.Hits.Hits {
		if v, ok := hit.Fields["event.original"]; ok {
			if s := fieldAsString(v); s != "" {
				logs = append(logs, s)
			}
		}
	}
	logs = append(logs, fmt.Sprintf("---\nStay %d logs to retrieve", totalHits-int64(len(searchRes.Hits.Hits))))

	return strings.Join(logs, "\n"), nil
}

// InvokeAsStream executes the OpensearchLogKubernetesTool with the given parameters and streams the logs line-by-line as a schema.StreamReader[string].
func (t *OpensearchLogKubernetesTool) InvokeAsStream(ctx context.Context, params *OpensearchLogKubernetesParams) (stream *schema.StreamReader[string], err error) {
	searchRes, err := t.runSearch(ctx, params)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		if searchRes == nil || searchRes.Hits == nil || len(searchRes.Hits.Hits) == 0 {
			logrus.Debug("No result found")
			sw.Send("No result found", nil)
			return
		}

		var totalHits int64
		if searchRes.Hits.TotalHits != nil {
			totalHits = searchRes.Hits.TotalHits.Value
		}

		logrus.Debugf("Total log available %d logs", totalHits)
		logrus.Debugf("Retrieved %d logs", len(searchRes.Hits.Hits))
		for _, hit := range searchRes.Hits.Hits {
			if v, ok := hit.Fields["event.original"]; ok {
				if s := fieldAsString(v); s != "" {
					sw.Send(s, nil)
				}
			}
		}

		sw.Send(fmt.Sprintf("---\nStay %d logs to retrieve", totalHits-int64(len(searchRes.Hits.Hits))), nil)
	}()

	return sr, nil
}

// buildQuery builds the OpenSearch bool query from the given parameters.
func (t *OpensearchLogKubernetesTool) buildQuery(params *OpensearchLogKubernetesParams) querydsl.Query {
	boolQuery := querydsl.NewBoolQuery()
	boolQuery.Must(querydsl.NewRangeQuery("@timestamp").Gte(params.From).Lte(params.To))
	boolQuery.Must(querydsl.NewTermQuery("labels.cluster", params.Cluster))
	boolQuery.Must(querydsl.NewTermQuery("kubernetes.namespace", params.Namespace))
	if params.PodName != "" {
		boolQuery.Must(querydsl.NewTermQuery("kubernetes.pod.name", params.PodName))
	}
	if params.ContainerName != "" {
		boolQuery.Must(querydsl.NewTermQuery("kubernetes.container.name", params.ContainerName))
	}
	if params.LuceneQuery == "" {
		params.LuceneQuery = "*"
	}
	stringQuery := querydsl.NewQueryStringQuery(params.LuceneQuery).WithAnalyzeWildcard(true)
	boolQuery.Must(stringQuery)
	return boolQuery
}

const defaultOpensearchTimeout = 30 * time.Second

// fieldAsString extracts a string value from an OpenSearch field, which may be
// returned as a plain string or as a []interface{} slice (the common wire format).
func fieldAsString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []interface{}:
		if len(val) > 0 {
			if s, ok := val[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// NewOpensearchLogKubernetesTool creates a new instance of the OpensearchLogKubernetesTool.
func NewOpensearchLogKubernetesTool(ctx context.Context, cfg *config.Config) (*OpensearchLogKubernetesTool, error) {

	c, err := NewClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Opensearch client")
	}

	opensearchLogKubernetesTool := &OpensearchLogKubernetesTool{
		client: c,
	}

	// Infer tool (non-streaming)
	t, err := utils.InferTool("opensearch_log_kubernetes_tool", opensearchLogKubernetesDescription, opensearchLogKubernetesTool.Invoke)
	if err != nil {
		return nil, err
	}
	opensearchLogKubernetesTool.InvokableTool = t

	// Wire the streaming path.
	streamable, err := utils.InferStreamTool("opensearch_log_kubernetes_tool", opensearchLogKubernetesDescription, opensearchLogKubernetesTool.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	opensearchLogKubernetesTool.StreamableTool = streamable

	return opensearchLogKubernetesTool, nil
}
