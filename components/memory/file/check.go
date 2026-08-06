package file

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check performs a health check for the file-based memory backend,
// verifying directory access, conversation creation, append, list,
// and deletion.
func Check(ctx context.Context, cfg FileMemoryConfig) checkup.Results {
	var results checkup.Results

	fm, err := NewFileMemory(cfg)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "connect",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to create FileMemory").Error(),
		})
		return append(results, checkup.DependencyFailed("get_conversation", "append_message", "list_conversations", "delete_conversation")...)
	}
	results = append(results, checkup.Result{
		Component: "connect",
		Status:    checkup.StatusOK,
		Message:   "directory accessible and writable",
	})

	conv, err := fm.GetConversation(checkup.CheckUser, checkup.CheckConvID, true)
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
			Message:   "conversation created",
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

		ids, listErr := fm.ListConversations(checkup.CheckUser)
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

	delErr := fm.DeleteConversation(checkup.CheckUser, checkup.CheckConvID)
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

	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cleanupConversations(cleanupCtx, fm)

	return results
}

func cleanupConversations(ctx context.Context, fm memory.Memory) {
	ids, err := fm.ListConversations(checkup.CheckUser)
	if err != nil {
		return
	}
	for _, id := range ids {
		_ = fm.DeleteConversation(checkup.CheckUser, id)
	}
}
