package opensearch

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/sirupsen/logrus"

	osparser "github.com/webcenter-fr/eino-ext/components/document/parser/opensearch"
)

const loaderType = "OpensearchLoader"

// Config holds the loader configuration.
type Config struct {
	// URLs is the list of OpenSearch cluster URLs.
	URLs []string `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`

	// Username for basic authentication.
	Username string `validate:"omitempty" jsonschema:"description=Username for basic authentication"`

	// Password for basic authentication.
	Password string `validate:"omitempty" jsonschema:"description=Password for basic authentication"`

	// TLSSkipVerify controls whether TLS certificate verification is skipped.
	TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
}

// Loader implements document.Loader by loading documents from an OpenSearch
// index. The source URI is expected in the format:
//
//	opensearch://index_name?q=optional_query_string
type Loader struct {
	client opensearchv4.Client
	parser *osparser.Parser
}

// NewOpensearchLoader creates a new OpenSearch document loader.
func NewOpensearchLoader(ctx context.Context, config *Config) (*Loader, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}
	if len(config.URLs) == 0 {
		return nil, errors.New("at least one URL is required")
	}

	opensearchCfg := &opensearchv4.Config{
		URL:           config.URLs[0],
		Username:      config.Username,
		Password:      config.Password,
		TLSSkipVerify: config.TLSSkipVerify,
	}

	logger := logrus.NewEntry(logrus.StandardLogger())
	client, err := opensearchv4.New(opensearchCfg, logger)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch client")
	}

	parser, err := osparser.NewParser(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create parser")
	}

	return &Loader{client: client, parser: parser}, nil
}

// GetType returns the component type identifier.
func (l *Loader) GetType() string {
	return loaderType
}

// IsCallbacksEnabled reports that this loader supports callbacks.
func (l *Loader) IsCallbacksEnabled() bool {
	return true
}

// Load reads documents from an OpenSearch index.
//
// The source URI is expected to be in the form:
//
//	opensearch://index_name?q=optional_query_string
//
// If no query string is provided, a match_all query is used.
func (l *Loader) Load(ctx context.Context, src document.Source, opts ...document.LoaderOption) ([]*schema.Document, error) {
	index, query := uriToIndexAndQuery(src.URI)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []map[string]any{
					{
						"query_string": map[string]any{
							"query":            query,
							"default_operator": "AND",
						},
					},
				},
			},
		},
		"size": 10000,
	}

	result, err := l.client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body:    body,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to search OpenSearch")
	}

	if result.Hits == nil || len(result.Hits.Hits) == 0 {
		return nil, nil
	}

	docs := make([]*schema.Document, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		src := map[string]any{}
		if hit.Source != nil {
			_ = json.Unmarshal(hit.Source, &src)
		}
		doc, err := l.parser.ConvertHit(src, hit.Id, hit.Index, hit.Score, hit.Version)
		if err != nil {
			return nil, err
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

// uriToIndexAndQuery parses a URI into an index name and optional query string.
// The URI is expected to be in the form:
//
//	opensearch://index_name?q=optional_query_string
func uriToIndexAndQuery(uriStr string) (index string, query string) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return uriStr, "*"
	}

	// Strip the scheme to get the index name
	index = u.Host
	if index == "" {
		index = uriStr
	}

	query = u.Query().Get("q")
	if query == "" {
		query = "*"
	}

	return index, query
}

// Compile-time interface check.
var _ document.Loader = (*Loader)(nil)
