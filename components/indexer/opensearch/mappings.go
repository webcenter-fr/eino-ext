package opensearch

import (
	"bytes"
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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
func EnsureMappings(ctx context.Context, client *opensearchapi.Client, index string, properties map[string]any) error {
	res, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{index}})
	if err != nil {
		// The v4 SDK returns an error even on a 404 (index not found); treat
		// that as "nothing to retrofit yet".
		if res != nil && res.StatusCode == 404 {
			return nil
		}
		return errors.Wrap(err, "failed to check index existence")
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode == 404 {
		return nil
	}

	body, err := json.Marshal(map[string]any{"properties": properties})
	if err != nil {
		return errors.Wrap(err, "failed to marshal mapping update")
	}

	if _, err = client.Indices.Mapping.Put(ctx, opensearchapi.MappingPutReq{
		Indices: []string{index},
		Body:    bytes.NewReader(body),
	}); err != nil {
		return errors.Wrap(err, "failed to update index mapping")
	}
	return nil
}
