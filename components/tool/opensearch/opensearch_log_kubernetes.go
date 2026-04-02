package opensearch

import (
	"context"
	"strings"

	"github.com/goccy/go-json"
	"github.com/sirupsen/logrus"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/disaster37/opensearch/v3"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/go-playground/validator/v10"
)

const opensearchLogKubernetesDescription = `
** General Purpose **
It permit to retrive logs from Opensearch about pods in Kubernetes cluster.
It useful to get logs when pod no more exist in Kubernetes cluster, but logs still exist in Opensearch.
It usefull to filter logs with lucene query syntax.

** Parameters *
You need to provide podName and or containerName.
Never put on luceneQuery the cluster, namespace, podName or containerName, because they are already filter by dedicated parameters, and put them in luceneQuery can cause issue with query performance.

** Output **
It returns the logs in string format.
`

// OpensearchLogKubernetesParams defines the parameters for the OpensearchLogKubernetes function.
type OpensearchLogKubernetesParams struct {
	Cluster       string `json:"cluster" validate:"required" jsonschema:"(required) The Kubernetes cluster to retrieve logs from."`
	Namespace     string `json:"namespace" validate:"required" jsonschema:"(required) The namespace of the pods to retrieve logs from."`
	PodName       string `json:"podName" jsonschema:"(optional) The name of the pod to retrieve logs from."`
	ContainerName string `json:"containerName" jsonschema:"(optional) The name of the container to retrieve logs from."`
	LuceneQuery   string `json:"luceneQuery" jsonschema:"(optional) The Lucene query to filter logs."`
	MaxLines      int64  `json:"maxLines,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The maximum number of log lines to return. Default to 100."`
}

// OpensearchLogKubernetesTool is a tool that retrieves logs from Opensearch about pods in a Kubernetes cluster. It implements the InvokableTool interface.
type OpensearchLogKubernetesTool struct {
	tool.InvokableTool
	tool.StreamableTool
	client *opensearch.Client
}

// Invoke executes the DescribeTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *OpensearchLogKubernetesTool) Invoke(ctx context.Context, params *OpensearchLogKubernetesParams) (result string, err error) {

	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return "", errors.Wrap(err, "invalid parameters for OpensearchLogKubernetesTool")
	}
	if params.PodName == "" && params.ContainerName == "" {
		return "", errors.New("at least one of podName or containerName must be provided")
	}

	boolQuery := opensearch.NewBoolQuery()
	boolQuery.Must(opensearch.NewTermQuery("labels.cluster", params.Cluster))
	boolQuery.Must(opensearch.NewTermQuery("kubernetes.namespace", params.Namespace))
	if params.PodName != "" {
		boolQuery.Must(opensearch.NewTermQuery("kubernetes.pod.name", params.PodName))
	}
	if params.ContainerName != "" {
		boolQuery.Must(opensearch.NewTermQuery("kubernetes.container.name", params.ContainerName))
	}
	if params.LuceneQuery == "" {
		params.LuceneQuery = "*"
	}
	stringQuery := opensearch.NewQueryStringQuery(params.LuceneQuery).AnalyzeWildcard(true)
	boolQuery.Must(stringQuery)

	res, err := t.client.Search("logs-*").
		Query(boolQuery).
		FetchSource(false).
		DocvalueFields(
			"event.original",
		).
		Size(int(params.MaxLines)).
		Do(ctx)

	if err != nil {
		return "", errors.Wrap(err, "failed to search logs in Opensearch")
	}

	if res.Hits.TotalHits.Value == 0 {
		logrus.Debug("No result found")
		return "No result found", nil
	}

	source := map[string]any{}
	logs := make([]string, 0, len(res.Hits.Hits))

	logrus.Debugf("Found %d logs", res.Hits.TotalHits.Value)
	for _, hit := range res.Hits.Hits {
		if err = json.Unmarshal(hit.Source, source); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal log source")
		}
		logs = append(logs, source["event.original"].(string))

	}

	return strings.Join(logs, "\n\n"), nil
}

// Invoke executes the DescribeTool with the given parameters. It validates the parameters, retrieves the appropriate Kubernetes client for the specified cluster, and lists the resources based on the provided namespace and label selector. The output is filtered using a regex pattern if provided, and the final result is returned as a JSON string.
func (t *OpensearchLogKubernetesTool) InvokeAsStream(ctx context.Context, params *OpensearchLogKubernetesParams) (stream *schema.StreamReader[string], err error) {

	if params.MaxLines == 0 {
		params.MaxLines = 100
	}
	validator := validator.New()
	if err := validator.Struct(params); err != nil {
		return nil, errors.Wrap(err, "invalid parameters for OpensearchLogKubernetesTool")
	}
	if params.PodName == "" && params.ContainerName == "" {
		return nil, errors.New("at least one of podName or containerName must be provided")
	}

	boolQuery := opensearch.NewBoolQuery()
	boolQuery.Must(opensearch.NewTermQuery("labels.cluster", params.Cluster))
	boolQuery.Must(opensearch.NewTermQuery("kubernetes.namespace", params.Namespace))
	if params.PodName != "" {
		boolQuery.Must(opensearch.NewTermQuery("kubernetes.pod.name", params.PodName))
	}
	if params.ContainerName != "" {
		boolQuery.Must(opensearch.NewTermQuery("kubernetes.container.name", params.ContainerName))
	}
	if params.LuceneQuery != "" {
		stringQuery := opensearch.NewQueryStringQuery(params.LuceneQuery).AnalyzeWildcard(true)
		boolQuery.Must(stringQuery)
	}

	res, err := t.client.Search("logs-*").
		Query(boolQuery).
		FetchSource(false).
		DocvalueFields(
			"event.original",
		).
		Size(int(params.MaxLines)).Do(ctx)

	if err != nil {
		return nil, errors.Wrap(err, "failed to search logs in Opensearch")
	}

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		if res.Hits.TotalHits.Value == 0 {
			sw.Send("No result found", nil)
			return
		}

		source := map[string]any{}
		for _, hit := range res.Hits.Hits {
			if err = json.Unmarshal(hit.Source, source); err != nil {
				sw.Send("", errors.Wrap(err, "failed to unmarshal log source"))
				return
			}
			sw.Send(source["event.original"].(string), nil)
		}
	}()

	return sr, nil
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

	// Infer tool
	t, err := utils.InferTool("opensearch_log_kubernetes_tool", opensearchLogKubernetesDescription, opensearchLogKubernetesTool.Invoke)
	if err != nil {
		return nil, err
	}
	opensearchLogKubernetesTool.InvokableTool = t

	return opensearchLogKubernetesTool, nil
}
