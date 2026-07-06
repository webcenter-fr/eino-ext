package opensearch

import (
	"context"

	"emperror.dev/errors"
	"github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
)

// EnsureMappings merges the given field properties into an already-existing
// index via `PUT _mapping`. It is a no-op when the index does not exist yet
// (the caller is expected to create it with its full mapping elsewhere), and
// a safe, idempotent merge otherwise: the OpenSearch put-mapping API only adds
// new fields / sub-fields, it never removes or changes the type of existing
// ones.
//
// `properties` is the value of the mapping "properties" object, e.g.:
//
//	map[string]any{
//	    "source_id":   map[string]any{"type": "keyword"},
//	    "source_hash": map[string]any{"type": "keyword"},
//	}
func EnsureMappings(ctx context.Context, client opensearch.Client, index string, properties map[string]any) error {
	exists, err := client.Indices().Exists(ctx, []string{index})
	if err != nil {
		return errors.Wrap(err, "failed to check index existence")
	}
	if !exists {
		return nil
	}

	if _, err = client.Indices().PutMapping(ctx, &api.PutMappingRequest{
		Indices: []string{index},
		Body:    map[string]any{"properties": properties},
	}); err != nil {
		return errors.Wrap(err, "failed to update index mapping")
	}
	return nil
}
