package confirm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func TestRequireConfirmationCtx(t *testing.T) {
	authorizedCtx := safety.WithExecutionAuthorized(context.Background(), "tool")

	if err := RequireConfirmationCtx(context.Background(), "tool", true, false); err != nil {
		t.Fatalf("dryRun should always pass, got %v", err)
	}

	err := RequireConfirmationCtx(context.Background(), "tool", false, false)
	if err == nil || !strings.Contains(err.Error(), "confirmed must be true") {
		t.Fatalf("expected 'confirmed must be true' error, got %v", err)
	}

	if err := RequireConfirmationCtx(authorizedCtx, "tool", false, true); err != nil {
		t.Fatalf("authorized ctx should pass, got %v", err)
	}

	if err := RequireConfirmationCtx(context.Background(), "tool", false, true); !errors.Is(err, safety.ErrExecutionNotAuthorized) {
		t.Fatalf("expected ErrExecutionNotAuthorized, got %v", err)
	}

	if err := RequireConfirmationCtx(nil, "tool", false, true); !errors.Is(err, safety.ErrExecutionNotAuthorized) {
		t.Fatalf("expected ErrExecutionNotAuthorized for nil ctx, got %v", err)
	}
}

func TestRequireConfirmationForActionCtx(t *testing.T) {
	err := RequireConfirmationForActionCtx(context.Background(), "tool", "do thing", false)
	if err == nil || !strings.Contains(err.Error(), "Confirmed must be true") {
		t.Fatalf("expected 'Confirmed must be true' error, got %v", err)
	}

	authorizedCtx := safety.WithExecutionAuthorized(context.Background(), "tool")
	if err := RequireConfirmationForActionCtx(authorizedCtx, "tool", "do thing", true); err != nil {
		t.Fatalf("authorized ctx should pass, got %v", err)
	}

	if err := RequireConfirmationForActionCtx(context.Background(), "tool", "do thing", true); !errors.Is(err, safety.ErrExecutionNotAuthorized) {
		t.Fatalf("expected ErrExecutionNotAuthorized, got %v", err)
	}
}
