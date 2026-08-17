package opensearch_retriever

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/tool"
)

// ClusterConfig holds shared cluster-level settings for OpenSearch retriever tools.
type ClusterConfig struct {
	URLs          []string
	Username      string
	Password      string
	TLSSkipVerify bool

	Embedder             embedding.Embedder
	VectorField          string
	ContentField         string
	Hybrid               bool
	K                    int
	SearchPipeline       string
	EnsureSearchPipeline bool
	Formatter            HitFormatter
}

// IndexConfig holds per-index settings for a retriever tool.
type IndexConfig struct {
	Index        string
	ToolName     string
	Description  string
	DefaultTopK  int
	HeaderFields []HeaderField
}

// NewAllTools creates retriever tools for all configured indices using a
// shared cluster connection.
func NewAllTools(ctx context.Context, cluster ClusterConfig, indices []IndexConfig) ([]tool.InvokableTool, error) {
	if len(indices) == 0 {
		return nil, errors.New("at least one index config is required")
	}

	tools := make([]tool.InvokableTool, 0, len(indices))

	for _, idx := range indices {
		cfg := &Config{
			URLs:                 cluster.URLs,
			Username:             cluster.Username,
			Password:             cluster.Password,
			TLSSkipVerify:        cluster.TLSSkipVerify,
			Index:                idx.Index,
			Embedder:             cluster.Embedder,
			VectorField:          cluster.VectorField,
			ContentField:         cluster.ContentField,
			Hybrid:               cluster.Hybrid,
			K:                    cluster.K,
			SearchPipeline:       cluster.SearchPipeline,
			EnsureSearchPipeline: cluster.EnsureSearchPipeline,
			ToolName:             idx.ToolName,
			Description:          idx.Description,
			DefaultTopK:          idx.DefaultTopK,
			Formatter:            cluster.Formatter,
			HeaderFields:         idx.HeaderFields,
		}

		t, err := NewTool(ctx, cfg)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create tool for index %q", idx.Index)
		}

		tools = append(tools, t)
	}

	return tools, nil
}
