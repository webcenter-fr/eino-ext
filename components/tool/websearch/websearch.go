package websearch

import (
	"context"
	_ "embed"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

//go:embed prompts/web_search.md
var webSearchDescription string

// WebSearchParams are the input parameters for the web_search tool.
type WebSearchParams struct {
	Query      string `json:"query" validate:"required" jsonschema:"(required) The search query string."`
	NumResults int    `json:"numResults,omitempty" validate:"omitempty,min=1,max=20" jsonschema:"(optional, default 10, max 20) Number of results to return."`
}

// WebSearchTool is an invokable tool that performs web searches via DuckDuckGo.
type WebSearchTool struct {
	cfg Config
	tool.InvokableTool
}

// Invoke performs the web search.
func (t *WebSearchTool) Invoke(ctx context.Context, params *WebSearchParams) (string, error) {
	if params.NumResults <= 0 {
		params.NumResults = 10
	}
	if params.NumResults > 20 {
		params.NumResults = 20
	}

	results, err := search(ctx, params.Query, t.cfg)
	if err != nil {
		return "", errors.Wrap(err, "web search failed")
	}

	// Truncate to the requested number of results.
	if len(results) > params.NumResults {
		results = results[:params.NumResults]
	}

	output, err := json.Marshal(results)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal search results")
	}

	return string(output), nil
}

// NewWebSearchTool creates a new web_search tool.
func NewWebSearchTool(cfg *Config) (tool.InvokableTool, error) {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	// Make a local copy so the caller's config is not mutated.
	localCfg := cfg.applyDefaults(DefaultConfig())

	searchTool := &WebSearchTool{
		cfg: localCfg,
	}

	invokable, err := utils.InferTool("web_search", webSearchDescription, searchTool.Invoke)
	if err != nil {
		return nil, err
	}
	searchTool.InvokableTool = invokable

	return searchTool, nil
}
