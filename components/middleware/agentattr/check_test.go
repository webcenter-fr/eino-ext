package agentattr

import (
	"context"
	"testing"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheckReturnsOK(t *testing.T) {
	results := Check(context.Background(), nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != checkup.StatusOK {
		t.Errorf("expected status ok, got %s", results[0].Status)
	}
	if results[0].Component != "agentattr" {
		t.Errorf("expected component agentattr, got %s", results[0].Component)
	}
	if !results.OK() {
		t.Error("expected OK() to be true")
	}
}
