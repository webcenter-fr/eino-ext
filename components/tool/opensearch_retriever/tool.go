// Package opensearch_retriever provides a generic tool.InvokableTool that wraps
// the existing OpenSearch retriever, enabling semantic document search via
// natural-language queries as an invokable tool.
package opensearch_retriever

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	retrieveropensearch "github.com/webcenter-fr/eino-ext/components/retriever/opensearch"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

type Config struct {
	URLs          []string `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`
	Username      string   `validate:"omitempty" jsonschema:"description=Username for basic authentication"`
	Password      string   `validate:"omitempty" jsonschema:"description=Password for basic authentication"`
	TLSSkipVerify bool     `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`

	Index                string             `validate:"required" jsonschema:"description=OpenSearch index to search"`
	Embedder             embedding.Embedder `validate:"-" jsonschema:"-"`
	VectorField          string             `validate:"omitempty" jsonschema:"description=knn_vector field name for kNN search"`
	ContentField         string             `validate:"omitempty" jsonschema:"description=Text field for BM25 match, defaults to content"`
	Hybrid               bool               `validate:"omitempty" jsonschema:"description=Combine kNN with BM25 on ContentField"`
	K                    int                `validate:"omitempty" jsonschema:"description=Number of nearest neighbors for kNN"`
	SearchPipeline       string             `validate:"omitempty" jsonschema:"description=Optional search pipeline name"`
	EnsureSearchPipeline bool               `validate:"omitempty" jsonschema:"description=Idempotently create SearchPipeline on startup"`

	ToolName    string `validate:"required" jsonschema:"description=Name for the tool"`
	Description string `validate:"required" jsonschema:"description=Tool description for the LLM"`
	DefaultTopK int    `validate:"omitempty" jsonschema:"description=Max results default, defaults to 5"`

	Formatter    HitFormatter  `validate:"-" jsonschema:"-"`
	HeaderFields []HeaderField `validate:"omitempty" jsonschema:"description=Metadata fields rendered as header lines before content"`
}

type HeaderField struct {
	MetaKey string `validate:"required" jsonschema:"description=Key in doc.MetaData"`
	Label   string `validate:"omitempty" jsonschema:"description=Display label, defaults to MetaKey"`
}

type Query struct {
	Query string `json:"query" validate:"required" jsonschema:"(required) natural-language search query"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results, defaults to configured DefaultTopK"`
}

type Tool struct {
	retriever   retriever.Retriever
	formatter   HitFormatter
	defaultTopK int
	tool.InvokableTool
}

var _ tool.InvokableTool = (*Tool)(nil)

func NewTool(ctx context.Context, cfg *Config) (*Tool, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.ContentField == "" {
		cfg.ContentField = "content"
	}
	if cfg.DefaultTopK == 0 {
		cfg.DefaultTopK = 5
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	retrieverCfg := &retrieveropensearch.Config{
		URLs:                 cfg.URLs,
		Username:             cfg.Username,
		Password:             cfg.Password,
		TLSSkipVerify:        cfg.TLSSkipVerify,
		Index:                cfg.Index,
		Embedding:            cfg.Embedder,
		VectorField:          cfg.VectorField,
		ContentField:         cfg.ContentField,
		Hybrid:               cfg.Hybrid,
		K:                    cfg.K,
		SearchPipeline:       cfg.SearchPipeline,
		EnsureSearchPipeline: cfg.EnsureSearchPipeline,
	}

	r, err := retrieveropensearch.NewRetriever(ctx, retrieverCfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch retriever")
	}

	var formatter HitFormatter = cfg.Formatter
	if formatter == nil {
		formatter = NewDefaultHitFormatter(cfg.HeaderFields)
	}

	t := &Tool{
		retriever:   r,
		formatter:   formatter,
		defaultTopK: cfg.DefaultTopK,
	}

	inv, err := utils.InferTool(cfg.ToolName, cfg.Description, t.Invoke)
	if err != nil {
		return nil, errors.Wrap(err, "failed to infer tool")
	}
	t.InvokableTool = inv

	return t, nil
}

func (t *Tool) Invoke(ctx context.Context, params *Query) (string, error) {
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	limit := params.Limit
	if limit <= 0 {
		limit = t.defaultTopK
	}

	docs, err := t.retriever.Retrieve(ctx, params.Query, retriever.WithTopK(limit))
	if err != nil {
		return "", errors.Wrap(err, "failed to retrieve documents")
	}

	if len(docs) == 0 {
		return "No documents found matching the query.", nil
	}

	var result string
	for i, doc := range docs {
		formatted := t.formatter.FormatHit(doc)
		if i > 0 {
			result += "\n---\n"
		}
		result += formatted
	}

	return result, nil
}
