package session

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

// ctx is reserved for future use (BeginTurn does not yet support context
// propagation).
func Check(ctx context.Context, sm *SessionManager) checkup.Results {
	if sm == nil {
		return checkup.Results{
			{
				Component: "session",
				Status:    checkup.StatusError,
				Error:     "SessionManager is nil",
			},
		}
	}

	msg := &schema.Message{
		Role:    schema.User,
		Content: "checkup probe",
	}

	turn, err := sm.BeginTurn(checkup.CheckUser, checkup.CheckConvID, msg)
	if err != nil {
		return checkup.Results{
			{
				Component: "session",
				Status:    checkup.StatusError,
				Error:     errors.Wrap(err, "failed to begin turn").Error(),
			},
		}
	}

	turn.Discard()

	return checkup.Results{
		{
			Component: "session",
			Status:    checkup.StatusOK,
			Message:   "BeginTurn + Discard succeeded",
		},
	}
}
