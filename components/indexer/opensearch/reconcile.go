package opensearch

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
)

const (
	DefaultSourceIDField   = "source_id"
	DefaultSourceHashField = "source_hash"

	reconcileScrollBatchSize = 1000
	reconcileDeleteBatchSize = 500
	reconcileScrollKeepAlive = "2m"
)

// options holds the resolved field names used by the reconcile helpers.
type options struct {
	sourceIDField   string
	sourceHashField string
}

// Option customizes the reconcile helpers.
type Option func(*options)

// WithSourceIDField overrides the field holding the source id (default
// "source_id").
func WithSourceIDField(field string) Option {
	return func(o *options) { o.sourceIDField = field }
}

// WithSourceHashField overrides the field holding the content hash (default
// "source_hash").
func WithSourceHashField(field string) Option {
	return func(o *options) { o.sourceHashField = field }
}

func newOptions(opts ...Option) options {
	o := options{sourceIDField: DefaultSourceIDField, sourceHashField: DefaultSourceHashField}
	for _, f := range opts {
		f(&o)
	}
	return o
}

// ReconcileFilter optionally scopes both the scan of existing source ids and
// the deletion of missing ones (e.g. to a single value of a partition field).
type ReconcileFilter struct {
	Field string
	Value string
}

// LookupSourceHash returns the current source hash stored for a given source
// id. found is false if no document exists yet for that source id.
func LookupSourceHash(ctx context.Context, client opensearch.Client, index, sourceID string, opts ...Option) (hash string, found bool, err error) {
	o := newOptions(opts...)
	termQuery, err := querydsl.NewTermQuery(o.sourceIDField, sourceID).Source()
	if err != nil {
		return "", false, errors.Wrap(err, "failed to build source_id term query")
	}
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   termQuery,
			"size":    1,
			"_source": []string{o.sourceHashField},
		},
	})
	if err != nil {
		return "", false, errors.Wrap(err, "failed to search for existing source_id")
	}
	if result.Hits == nil || len(result.Hits.Hits) == 0 {
		return "", false, nil
	}
	src := map[string]any{}
	if err = json.Unmarshal(result.Hits.Hits[0].Source, &src); err != nil {
		return "", false, errors.Wrap(err, "failed to decode existing source hit")
	}
	hash, ok := src[o.sourceHashField].(string)
	if !ok {
		return "", true, errors.Errorf("source_hash field %q is not a string: %T", o.sourceHashField, src[o.sourceHashField])
	}
	return hash, true, nil
}

// BulkLookupSourceHashes scrolls every existing source id -> hash pair in the
// index (optionally scoped by filter) and returns them as a map.
func BulkLookupSourceHashes(ctx context.Context, client opensearch.Client, index string, filter *ReconcileFilter, opts ...Option) (map[string]string, error) {
	o := newOptions(opts...)
	query, err := reconcileQuery(filter)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string)
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   query,
			"size":    reconcileScrollBatchSize,
			"_source": []string{o.sourceIDField, o.sourceHashField},
		},
		Params: &api.SearchParams{Scroll: reconcileScrollKeepAlive},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start bulk source_hash scroll")
	}
	scrollID := result.ScrollId
	defer func() {
		if scrollID != "" {
			_, _ = client.Search().ClearScroll(ctx, []string{scrollID})
		}
	}()
	for {
		if result.Hits == nil || len(result.Hits.Hits) == 0 {
			break
		}
		for _, h := range result.Hits.Hits {
			src := map[string]any{}
			if unmarshalErr := json.Unmarshal(h.Source, &src); unmarshalErr != nil {
				return hashes, errors.Wrap(unmarshalErr, "failed to decode source hit during bulk lookup")
			}
			sid, _ := src[o.sourceIDField].(string)
			if sid == "" {
				continue
			}
			shash, _ := src[o.sourceHashField].(string)
			hashes[sid] = shash
		}
		result, err = client.Search().Scroll(ctx, &api.ScrollRequest{ScrollId: scrollID, KeepAlive: reconcileScrollKeepAlive})
		if err != nil {
			return hashes, errors.Wrap(err, "failed to continue bulk source_hash scroll")
		}
		scrollID = result.ScrollId
	}
	return hashes, nil
}

// DeleteBySourceID deletes every document whose source id matches the value.
func DeleteBySourceID(ctx context.Context, client opensearch.Client, index, sourceID string, opts ...Option) error {
	return DeleteBySourceIDs(ctx, client, index, []string{sourceID}, opts...)
}

// DeleteBySourceIDs deletes every document whose source id is in the list.
func DeleteBySourceIDs(ctx context.Context, client opensearch.Client, index string, sourceIDs []string, opts ...Option) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	o := newOptions(opts...)
	values := make([]any, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		values = append(values, id)
	}
	query, err := querydsl.NewTermsQuery(o.sourceIDField, values...).Source()
	if err != nil {
		return errors.Wrap(err, "failed to build source_id terms query")
	}
	resp, err := client.Document().DeleteByQuery(ctx, []string{index}, map[string]any{"query": query})
	if err != nil {
		return errors.Wrap(err, "failed to delete documents by source_id")
	}
	if len(resp.Failures) > 0 {
		return errors.Errorf("delete by source_id completed with failures: %+v", resp.Failures)
	}
	return nil
}

// Reconcile scrolls every existing source id (optionally scoped by filter) and
// deletes any that is not present in `seen`, in batches. Returns the number of
// deleted source ids.
func Reconcile(ctx context.Context, client opensearch.Client, index string, seen map[string]bool, filter *ReconcileFilter, opts ...Option) (deleted int, err error) {
	o := newOptions(opts...)
	query, err := reconcileQuery(filter)
	if err != nil {
		return 0, err
	}
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   query,
			"size":    reconcileScrollBatchSize,
			"_source": []string{o.sourceIDField},
		},
		Params: &api.SearchParams{Scroll: reconcileScrollKeepAlive},
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to start reconciliation scroll")
	}
	var toDelete []string
	scrollID := result.ScrollId
	defer func() {
		if scrollID != "" {
			_, _ = client.Search().ClearScroll(ctx, []string{scrollID})
		}
	}()
	for {
		if result.Hits == nil || len(result.Hits.Hits) == 0 {
			break
		}
		for _, h := range result.Hits.Hits {
			src := map[string]any{}
			if unmarshalErr := json.Unmarshal(h.Source, &src); unmarshalErr != nil {
				return deleted, errors.Wrap(unmarshalErr, "failed to decode source hit during reconciliation")
			}
			sid, _ := src[o.sourceIDField].(string)
			if sid == "" || seen[sid] {
				continue
			}
			toDelete = append(toDelete, sid)
		}
		result, err = client.Search().Scroll(ctx, &api.ScrollRequest{ScrollId: scrollID, KeepAlive: reconcileScrollKeepAlive})
		if err != nil {
			return deleted, errors.Wrap(err, "failed to continue reconciliation scroll")
		}
		scrollID = result.ScrollId
	}
	for i := 0; i < len(toDelete); i += reconcileDeleteBatchSize {
		end := i + reconcileDeleteBatchSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		if delErr := DeleteBySourceIDs(ctx, client, index, toDelete[i:end], opts...); delErr != nil {
			return deleted, delErr
		}
		deleted += end - i
	}
	return deleted, nil
}

func reconcileQuery(filter *ReconcileFilter) (any, error) {
	if filter == nil || filter.Field == "" {
		matchAll, err := querydsl.NewMatchAllQuery().Source()
		if err != nil {
			return nil, errors.Wrap(err, "failed to build match_all query")
		}
		return matchAll, nil
	}
	boolQuery, err := querydsl.NewBoolQuery().Filter(querydsl.NewTermQuery(filter.Field, filter.Value)).Source()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build reconciliation bool query")
	}
	return boolQuery, nil
}
