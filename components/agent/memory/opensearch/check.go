package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check performs a health check against the OpenSearch store.
func Check(ctx context.Context, cfg *Config, embedder embedding.Embedder) checkup.Results {
	var results checkup.Results

	if cfg == nil {
		cfg = &Config{}
	}

	baseCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	store, err := NewStore(baseCtx, cfg)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "connect",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to create Store").Error(),
		})
		return append(results, checkup.DependencyFailed("count", "list", "store", "retrieve", "delete", "delete_by_filter")...)
	}
	results = append(results, checkup.Result{
		Component: "connect",
		Status:    checkup.StatusOK,
		Message:   "Store created, index ensured",
	})

	count, err := store.Count(baseCtx)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "count",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to count documents").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "count",
			Status:    checkup.StatusOK,
			Message:   fmt.Sprintf("%d documents in store", count),
		})
	}

	docs, err := store.List(baseCtx, 0, 1)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "list",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list documents").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "list",
			Status:    checkup.StatusOK,
			Message:   fmt.Sprintf("listed %d documents", len(docs)),
		})
	}

	testDoc := &schema.Document{
		ID:      "checkup_test",
		Content: "This is a connectivity test document for the agent memory store checkup.",
		MetaData: map[string]any{
			"category":   "test",
			"source":     "checkup",
			"session_id": "checkup_session",
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	ids, err := store.Store(baseCtx, []*schema.Document{testDoc})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "store",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to store document").Error(),
		})
	} else if len(ids) == 0 {
		results = append(results, checkup.Result{
			Component: "store",
			Status:    checkup.StatusError,
			Error:     "no document IDs returned from store",
		})
	} else {
		testDoc.ID = ids[0]
		defer func() {
			_, _ = store.DeleteByFilter(context.Background(), map[string]any{"source": "checkup"})
		}()
		results = append(results, checkup.Result{
			Component: "store",
			Status:    checkup.StatusOK,
			Message:   fmt.Sprintf("document stored with id %q", ids[0]),
		})
	}

	retMsg := ""
	if embedder != nil {
		docs, retrieveErr := store.Retrieve(baseCtx, "connectivity test")
		if retrieveErr != nil {
			results = append(results, checkup.Result{
				Component: "retrieve",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(retrieveErr, "failed to retrieve documents").Error(),
			})
		} else {
			retMsg = fmt.Sprintf("retrieved %d documents via kNN+BM25", len(docs))
			results = append(results, checkup.Result{
				Component: "retrieve",
				Status:    checkup.StatusOK,
				Message:   retMsg,
			})
		}
	} else {
		docs, retrieveErr := store.Retrieve(baseCtx, "connectivity test")
		if retrieveErr != nil {
			results = append(results, checkup.Result{
				Component: "retrieve",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(retrieveErr, "failed to retrieve documents").Error(),
			})
		} else {
			retMsg = fmt.Sprintf("retrieved %d documents via BM25 (kNN not configured)", len(docs))
			results = append(results, checkup.Result{
				Component: "retrieve",
				Status:    checkup.StatusOK,
				Message:   retMsg,
			})
		}
	}

	if testDoc.ID != "checkup_test" {
		delErr := store.Delete(baseCtx, testDoc.ID)
		if delErr != nil {
			results = append(results, checkup.Result{
				Component: "delete",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(delErr, "failed to delete document").Error(),
			})
		} else {
			results = append(results, checkup.Result{
				Component: "delete",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("document %q deleted", testDoc.ID),
			})
		}

		deleted, delByFilterErr := store.DeleteByFilter(baseCtx, map[string]any{"source": "checkup"})
		if delByFilterErr != nil {
			results = append(results, checkup.Result{
				Component: "delete_by_filter",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(delByFilterErr, "failed to delete by filter").Error(),
			})
		} else {
			results = append(results, checkup.Result{
				Component: "delete_by_filter",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("deleted %d documents by filter", deleted),
			})
		}
	} else {
		results = append(results, checkup.Result{
			Component: "delete",
			Status:    checkup.StatusLimited,
			Message:   "store did not return an ID, cannot test delete",
		})
		results = append(results, checkup.Result{
			Component: "delete_by_filter",
			Status:    checkup.StatusLimited,
			Message:   "store did not return an ID, cannot test delete_by_filter",
		})
	}

	return results
}
