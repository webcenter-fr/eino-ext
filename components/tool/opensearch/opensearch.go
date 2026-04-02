package opensearch

import (
	"github.com/disaster37/opensearch/v3"
	"github.com/disaster37/opensearch/v3/config"
	"k8s.io/utils/ptr"
)

// NewClient creates a new OpenSearch client using the provided configuration. It sets the Sniff and Healthcheck options to false, and returns the initialized client or an error if the client creation fails.
func NewClient(cfg *config.Config) (*opensearch.Client, error) {

	cfg.Sniff = ptr.To(false)
	cfg.Healthcheck = ptr.To(false)

	es, err := opensearch.NewClientFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	return es, nil
}
