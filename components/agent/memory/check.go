package memory

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// Check performs a health check against the memory store and model, returning
// checkup results suitable for a readiness probe.
func Check(ctx context.Context, store MemoryStore, m model.BaseChatModel) checkup.Results {
	var results checkup.Results

	baseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if store == nil {
		results = append(results, checkup.Result{
			Component: "memoryagent_store",
			Status:    checkup.StatusError,
			Error:     "MemoryStore is nil",
		})
	} else {
		count, err := store.Count(baseCtx)
		if err != nil {
			results = append(results, checkup.Result{
				Component: "memoryagent_store",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(err, "failed to count documents").Error(),
			})
		} else {
			results = append(results, checkup.Result{
				Component: "memoryagent_store",
				Status:    checkup.StatusOK,
				Message:   fmt.Sprintf("store ready, %d documents", count),
			})
		}
	}

	if m == nil {
		results = append(results, checkup.Result{
			Component: "memoryagent_model",
			Status:    checkup.StatusError,
			Error:     "model is nil",
		})
		return results
	}

	msg := &schema.Message{
		Role:    schema.User,
		Content: "Say hello",
	}
	resp, err := m.Generate(baseCtx, []*schema.Message{msg})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "memoryagent_model",
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to generate response").Error(),
		})
	} else if resp == nil {
		results = append(results, checkup.Result{
			Component: "memoryagent_model",
			Status:    checkup.StatusError,
			Error:     "model returned nil response",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "memoryagent_model",
			Status:    checkup.StatusOK,
			Message:   "model responded, connectivity ok",
		})
	}

	return results
}
