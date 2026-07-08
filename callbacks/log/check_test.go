package log

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheck(t *testing.T) {
	results := Check(context.Background())
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %q", results[0].Status)
	}
	if results[0].Component != "callback_log_handler" {
		t.Errorf("expected component 'callback_log_handler', got %q", results[0].Component)
	}
}
