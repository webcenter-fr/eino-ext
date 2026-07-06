package opensearch

import (
	"fmt"

	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/sirupsen/logrus"
)

// NewClient creates a new OpenSearch v4 client using the provided v3-compatible configuration.
// It maps the v3 config.Config fields to the v4 opensearch.Config struct, creates a logger,
// and returns the initialized client or an error if the client creation fails.
func NewClient(cfg *config.Config) (opensearchv4.Client, error) {
	if len(cfg.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided in config")
	}

	opensearchCfg := &opensearchv4.Config{
		URL:          cfg.URLs[0],
		Username:     cfg.Username,
		Password:     cfg.Password,
		TLSSkipVerify: false,
	}

	logger := logrus.NewEntry(logrus.StandardLogger())

	es, err := opensearchv4.New(opensearchCfg, logger)
	if err != nil {
		return nil, err
	}

	return es, nil
}
