package opensearch

import (
	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

// NewClient creates a new OpenSearch v4 client using the provided v3-compatible
// configuration. It delegates to the shared osclient builder to keep the
// connection logic centralized.
func NewClient(cfg *config.Config) (opensearchv4.Client, error) {
	if cfg == nil || len(cfg.URLs) == 0 {
		return nil, errors.New("at least one OpenSearch URL is required")
	}
	return osclient.New(osclient.Config{
		URLs:     cfg.URLs,
		Username: cfg.Username,
		Password: cfg.Password,
	}, 0)
}
