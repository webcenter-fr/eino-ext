// Package opensearch provides an OpenSearch-backed memory store.
package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

// Check performs a health check for the OpenSearch memory backend,
// verifying connectivity, index operations, and read/write functionality.
func Check(ctx context.Context, cfg Config) checkup.Results {
	var results checkup.Results

	if cfg.IndexName == "" {
		cfg.IndexName = "eino_memory"
	}

	baseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	osClient, err := createClient(baseCtx, cfg)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "connect",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to create OpenSearch client").Error(),
		})
		return append(results, checkup.DependencyFailed("index_exists", "get_conversation", "append_message", "list_conversations", "delete_conversation")...)
	}

	mem, err := NewOpenSearchMemory(cfg)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "connect",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to create OpenSearchMemory").Error(),
		})
		return append(results, checkup.DependencyFailed("index_exists", "get_conversation", "append_message", "list_conversations", "delete_conversation")...)
	}
	results = append(results, checkup.Result{
		Component: "connect",
		Status:    checkup.StatusOK,
		Message:   "OpenSearch client created, index ensured",
	})

	exists, err := osClient.Indices().Exists(baseCtx, []string{cfg.IndexName})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "index_exists",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to check index existence").Error(),
		})
	} else if !exists {
		results = append(results, checkup.Result{
			Component: "index_exists",
			Status:    checkup.StatusError,
			Error:     fmt.Sprintf("index %q does not exist", cfg.IndexName),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "index_exists",
			Status:    checkup.StatusOK,
			Message:   fmt.Sprintf("index %q exists", cfg.IndexName),
		})
	}

	results = append(results, runCRUDProbes(baseCtx, mem)...)

	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cleanupCancel()
	cleanupConversations(cleanupCtx, mem)

	return results
}

func runCRUDProbes(ctx context.Context, mem memory.Memory) checkup.Results {
	var results checkup.Results

	conv, err := mem.GetConversation(checkup.CheckUser, checkup.CheckConvID, true)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "get_conversation",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to get conversation").Error(),
		})
		results = append(results, checkup.Result{
			Component: "append_message",
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
		results = append(results, checkup.Result{
			Component: "list_conversations",
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "get_conversation",
			Status:    checkup.StatusOK,
			Message:   "conversation created and retrieved",
		})

		before := len(conv.GetFullMessages())
		msg := &schema.Message{Role: schema.Assistant, Content: "checkup"}
		conv.Append(msg)
		after := len(conv.GetFullMessages())
		if after <= before {
			results = append(results, checkup.Result{
				Component: "append_message",
				Status:    checkup.StatusError,
				Error:     "message count did not increase after append",
			})
		} else {
			results = append(results, checkup.Result{
				Component: "append_message",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("messages grew from %d to %d", before, after),
			})
		}

		ids, listErr := mem.ListConversations(checkup.CheckUser)
		if listErr != nil {
			results = append(results, checkup.Result{
				Component: "list_conversations",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(listErr, "failed to list conversations").Error(),
			})
		} else {
			results = append(results, checkup.Result{
				Component: "list_conversations",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("%d conversations found", len(ids)),
			})
		}
	}

	delErr := mem.DeleteConversation(checkup.CheckUser, checkup.CheckConvID)
	if delErr != nil {
		results = append(results, checkup.Result{
			Component: "delete_conversation",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(delErr, "failed to delete conversation").Error(),
		})
	} else {
		results = append(results, checkup.Result{
			Component: "delete_conversation",
			Status:    checkup.StatusOK,
			Message:   "test conversation deleted",
		})
	}

	return results
}

func createClient(ctx context.Context, cfg Config) (opensearchv4.Client, error) {
	return osclient.New(ctx, osclient.Config{
		URLs:          cfg.URLs,
		Username:      cfg.Username,
		Password:      cfg.Password,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}, 0)
}

func cleanupConversations(ctx context.Context, mem memory.Memory) {
	ids, err := mem.ListConversations(checkup.CheckUser)
	if err != nil {
		return
	}
	for _, id := range ids {
		_ = mem.DeleteConversation(checkup.CheckUser, id)
	}
	_ = mem.DeleteConversation(checkup.CheckUser, checkup.CheckConvID)
}
