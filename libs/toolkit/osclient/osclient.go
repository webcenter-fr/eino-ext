// Package osclient provides a shared constructor for the eino-ext OpenSearch v4
// client (github.com/disaster37/opensearch/v4). It centralizes the connection
// configuration and error-wrapping convention duplicated across the OpenSearch
// indexer, retriever, loader and memory components.
//
// This is the eino-component OpenSearch client. It is distinct from the eino
// OpenSearch client scaffolding used by the retriever/indexer abstractions in
// upstream eino-ext.
package osclient

import (
	"context"
	"time"

	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/sirupsen/logrus"
)

// Config holds the connection fields shared by every OpenSearch component.
// Components embed it in their own Config so the tags stay consistent.
type Config struct {
	// URLs is the list of OpenSearch cluster URLs. The first entry is used.
	URLs []string `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`

	// Username for basic authentication.
	Username string `validate:"omitempty" jsonschema:"description=Username for basic authentication"`

	// Password for basic authentication.
	Password string `validate:"omitempty" jsonschema:"description=Password for basic authentication"`

	// TLSSkipVerify controls whether TLS certificate verification is skipped.
	TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
}

// New creates an OpenSearch v4 client from the shared configuration. A zero
// timeout leaves the client default in place.
func New(ctx context.Context, cfg Config, timeout time.Duration) (opensearchv4.Client, error) {
	if len(cfg.URLs) == 0 {
		return nil, errors.New("at least one OpenSearch URL is required")
	}

	opensearchCfg := &opensearchv4.Config{
		URL:           cfg.URLs[0],
		Username:      cfg.Username,
		Password:      cfg.Password,
		TLSSkipVerify: cfg.TLSSkipVerify,
		Timeout:       timeout,
	}

	logger := logrus.NewEntry(logrus.StandardLogger())
	client, err := opensearchv4.New(opensearchCfg, logger)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch client")
	}
	return client, nil
}
