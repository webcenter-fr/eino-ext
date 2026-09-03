package kubernetes

import (
	"context"
	"errors"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// TestResourceCreateUnauthorizedContext asserts the per-tool second layer:
// calling the tool directly (no middleware) with confirmed:true and an
// unauthorized context is refused with ErrExecutionNotAuthorized. The check
// fires right after validate.Struct and before any cluster resolution, so no
// live cluster or envtest is required — a rest.Config is only constructed,
// never dialed.
func TestResourceCreateUnauthorizedContext(t *testing.T) {
	ctx := context.Background()
	configs := Configs{
		"test": {Config: &rest.Config{Host: "http://127.0.0.1:1"}},
	}

	tool, err := NewResourceCreateTool(ctx, configs)
	if err != nil {
		t.Fatalf("NewResourceCreateTool: %v", err)
	}

	_, err = tool.InvokableRun(ctx, `{"cluster":"test","namespace":"default","kind":"ConfigMap","manifest":"{\"apiVersion\":\"v1\",\"kind\":\"ConfigMap\",\"metadata\":{\"name\":\"cm\",\"namespace\":\"default\"}}","confirmed":true}`)
	if !errors.Is(err, safety.ErrExecutionNotAuthorized) {
		t.Fatalf("expected ErrExecutionNotAuthorized, got %v", err)
	}
}

// Note on the WriteToolNames dry-run no-mutation contract: the kubernetes
// create/patch/apply/delete dry-run paths are enforced by the server-side
// DryRunAll option (metav1.DryRunAll) and the tool's early dry-run branches,
// which are covered by the envtest suite where envtest binaries are available.
// Where envtest is unavailable (e.g. CI without kubebuilder assets), the
// contract is guaranteed at the code level and by the grafana/argocd/shell
// contract tests.
