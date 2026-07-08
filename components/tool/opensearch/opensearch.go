package opensearch

import (
	"context"

	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

// NewClient creates a new OpenSearch v4 client from the shared connection
// configuration used across every eino-ext OpenSearch-backed component.
func NewClient(ctx context.Context, cfg *osclient.Config) (opensearchv4.Client, error) {
	if cfg == nil || len(cfg.URLs) == 0 {
		return nil, errors.New("at least one OpenSearch URL is required")
	}
	return osclient.New(ctx, *cfg, 0)
}
