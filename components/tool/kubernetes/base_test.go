package kubernetes

import (
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestGetExecTimeout(t *testing.T) {
	t.Run("no config falls back to default", func(t *testing.T) {
		b := &baseTool{configs: Configs{}}
		if got := b.getExecTimeout("missing"); got != defaultExecTimeout {
			t.Fatalf("expected default %s, got %s", defaultExecTimeout, got)
		}
	})

	t.Run("empty DefaultTimeout falls back to default", func(t *testing.T) {
		b := &baseTool{configs: Configs{
			"c1": {Config: &rest.Config{}},
		}}
		if got := b.getExecTimeout("c1"); got != defaultExecTimeout {
			t.Fatalf("expected default %s, got %s", defaultExecTimeout, got)
		}
	})

	t.Run("per-cluster DefaultTimeout wins", func(t *testing.T) {
		b := &baseTool{configs: Configs{
			"c1": {Config: &rest.Config{}, DefaultTimeout: "90s"},
		}}
		if got := b.getExecTimeout("c1"); got != 90*time.Second {
			t.Fatalf("expected 90s, got %s", got)
		}
	})

	t.Run("invalid DefaultTimeout falls back to default", func(t *testing.T) {
		b := &baseTool{configs: Configs{
			"c1": {Config: &rest.Config{}, DefaultTimeout: "not-a-duration"},
		}}
		if got := b.getExecTimeout("c1"); got != defaultExecTimeout {
			t.Fatalf("expected default %s, got %s", defaultExecTimeout, got)
		}
	})
}
