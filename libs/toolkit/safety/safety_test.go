/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- Gate tests ---

func TestExtractGateParams(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantDR  bool
		wantCf  bool
		wantErr bool
	}{
		{name: "both false", json: `{"dryRun":false,"confirmed":false}`, wantDR: false, wantCf: false},
		{name: "dryRun true", json: `{"dryRun":true}`, wantDR: true, wantCf: false},
		{name: "confirmed true", json: `{"confirmed":true}`, wantDR: false, wantCf: true},
		{name: "both true", json: `{"dryRun":true,"confirmed":true}`, wantDR: true, wantCf: true},
		{name: "missing fields", json: `{}`, wantDR: false, wantCf: false},
		{name: "invalid json", json: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gp, err := ExtractGateParams(tt.json)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if gp.DryRun != tt.wantDR {
					t.Fatalf("DryRun: want %v, got %v", tt.wantDR, gp.DryRun)
				}
				if gp.Confirmed != tt.wantCf {
					t.Fatalf("Confirmed: want %v, got %v", tt.wantCf, gp.Confirmed)
				}
			}
		})
	}
}

func TestShouldGate(t *testing.T) {
	writeTools := map[string]bool{"create": true, "delete": true}

	tests := []struct {
		name      string
		toolName  string
		gp        GateParams
		wantError bool
	}{
		{name: "read tool passes", toolName: "list", wantError: false},
		{name: "write dryRun passes", toolName: "create", gp: GateParams{DryRun: true}, wantError: false},
		{name: "write confirmed passes", toolName: "create", gp: GateParams{Confirmed: true}, wantError: false},
		{name: "write both passes", toolName: "create", gp: GateParams{DryRun: true, Confirmed: true}, wantError: false},
		{name: "write neither fails", toolName: "create", gp: GateParams{}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ShouldGate(tt.toolName, writeTools, tt.gp)
			if tt.wantError && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantError && !strings.Contains(err.Error(), "SAFETY GATE") {
				t.Fatalf("expected SAFETY GATE in error, got: %v", err)
			}
		})
	}
}

// --- Audit tests ---

func TestChannelSink(t *testing.T) {
	sink := NewChannelSink(5)
	defer sink.Close()

	event := AuditEvent{ToolName: "test", Phase: PhaseRead}
	if err := sink.Write(context.Background(), event); err != nil {
		t.Fatalf("Write: %v", err)
	}

	received := <-sink.Events()
	if received.ToolName != "test" {
		t.Fatalf("expected 'test', got %q", received.ToolName)
	}
}

func TestChannelSinkDefaultBuffer(t *testing.T) {
	sink := NewChannelSink(0)
	defer sink.Close()
	// Should create with buffer size 100 (or at least not panic).
	if sink == nil {
		t.Fatal("expected non-nil sink")
	}
}

func TestChannelSinkOverflow(t *testing.T) {
	sink := NewChannelSink(1)
	defer sink.Close()

	// Fill the buffer.
	if err := sink.Write(context.Background(), AuditEvent{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Overflow should not block — event is dropped.
	if err := sink.Write(context.Background(), AuditEvent{}); err != nil {
		t.Fatalf("Write should not error on overflow: %v", err)
	}

	// Drain one event (the first one).
	<-sink.Events()
}

func TestAuditSinkFunc(t *testing.T) {
	called := false
	fn := AuditSinkFunc(func(ctx context.Context, event AuditEvent) error {
		called = true
		if event.ToolName != "test" {
			t.Fatalf("expected 'test', got %q", event.ToolName)
		}
		return nil
	})
	if err := fn.Write(context.Background(), AuditEvent{ToolName: "test"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !called {
		t.Fatal("expected function to be called")
	}
}

func TestLogSink(t *testing.T) {
	sink := &LogSink{}
	// LogSink should not error.
	if err := sink.Write(context.Background(), AuditEvent{ToolName: "test", Phase: PhaseRead}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func TestAuditEventJSON(t *testing.T) {
	event := AuditEvent{
		ToolName:   "test_tool",
		CallID:     "abc-123",
		Phase:      PhaseExecute,
		Arguments:  json.RawMessage(`{"key":"value"}`),
		Result:     "done",
		PolicyPass: true,
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"toolName":"test_tool"`) {
		t.Fatalf("expected toolName in JSON, got: %s", string(data))
	}
}

// --- Policy tests ---

func TestCELPolicyBasic(t *testing.T) {
	rules := []CELRule{{
		Name:       "test-rule",
		Expression: `params.x == 1`,
	}}
	pol, err := NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	// Should pass.
	if err := pol.Evaluate(context.Background(), "any_tool", map[string]any{"x": float64(1)}); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}

	// Should fail.
	if err := pol.Evaluate(context.Background(), "any_tool", map[string]any{"x": float64(2)}); err == nil {
		t.Fatal("expected policy denial")
	}
}

func TestCELPolicyToolNameCondition(t *testing.T) {
	rules := []CELRule{{
		Name:       "only-create",
		Expression: `toolName == "create" || toolName == "read"`,
	}}
	pol, err := NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	if err := pol.Evaluate(context.Background(), "create", map[string]any{}); err != nil {
		t.Fatalf("create should pass: %v", err)
	}
	if err := pol.Evaluate(context.Background(), "read", map[string]any{}); err != nil {
		t.Fatalf("read should pass: %v", err)
	}
	if err := pol.Evaluate(context.Background(), "delete", map[string]any{}); err == nil {
		t.Fatal("delete should fail")
	}
}

func TestCELPolicyToolScoped(t *testing.T) {
	rules := []CELRule{{
		Name:       "scoped-rule",
		Expression: `params.allowed == true`,
		ToolNames:  []string{"scoped_tool"},
	}}
	pol, err := NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	// Non-scoped tool should pass regardless of params.
	if err := pol.Evaluate(context.Background(), "other_tool", map[string]any{}); err != nil {
		t.Fatalf("other_tool should pass: %v", err)
	}

	// Scoped tool with wrong params should fail.
	if err := pol.Evaluate(context.Background(), "scoped_tool", map[string]any{"allowed": false}); err == nil {
		t.Fatal("scoped_tool should fail")
	}
}

func TestCELPolicyNamespaceGuard(t *testing.T) {
	rules := []CELRule{{
		Name:       "no-kube-system",
		Expression: `!(params.namespace == "kube-system")`,
	}}
	pol, err := NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	if err := pol.Evaluate(context.Background(), "any", map[string]any{"namespace": "kube-system"}); err == nil {
		t.Fatal("expected denial for kube-system")
	}
	if err := pol.Evaluate(context.Background(), "any", map[string]any{"namespace": "default"}); err != nil {
		t.Fatalf("expected pass for default: %v", err)
	}
}

func TestCELPolicyEmptyRules(t *testing.T) {
	_, err := NewCELPolicy(nil)
	if err == nil {
		t.Fatal("expected error for nil rules")
	}
	_, err = NewCELPolicy([]CELRule{})
	if err == nil {
		t.Fatal("expected error for empty rules")
	}
}

func TestCELPolicyInvalidExpression(t *testing.T) {
	_, err := NewCELPolicy([]CELRule{{
		Name:       "bad",
		Expression: "this is not valid CEL @#$%",
	}})
	if err == nil {
		t.Fatal("expected compilation error for invalid expression")
	}
}

func TestPolicyChain(t *testing.T) {
	r1 := &CELRule{Name: "r1", Expression: `params.x > 0`}
	r2 := &CELRule{Name: "r2", Expression: `params.y > 0`}
	p1, _ := NewCELPolicy([]CELRule{*r1})
	p2, _ := NewCELPolicy([]CELRule{*r2})

	chain := PolicyChain{p1, p2}

	// Both pass.
	if err := chain.Evaluate(context.Background(), "any", map[string]any{"x": float64(1), "y": float64(1)}); err != nil {
		t.Fatalf("both should pass: %v", err)
	}

	// First fails.
	if err := chain.Evaluate(context.Background(), "any", map[string]any{"x": float64(0), "y": float64(1)}); err == nil {
		t.Fatal("first policy should fail")
	}

	// Second fails.
	if err := chain.Evaluate(context.Background(), "any", map[string]any{"x": float64(1), "y": float64(0)}); err == nil {
		t.Fatal("second policy should fail")
	}
}

// --- Ownership tests ---

func TestCheckOwnershipUnmanaged(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
		},
	}
	info := CheckOwnership(pod)
	if info.IsManaged {
		t.Fatal("expected unmanaged")
	}
}

func TestCheckOwnershipOwnerRef(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       "my-rs",
			}},
		},
	}
	info := CheckOwnership(pod)
	if !info.IsManaged {
		t.Fatal("expected managed due to ownerReferences")
	}
	if len(info.OwnerReferences) != 1 {
		t.Fatalf("expected 1 ownerReference, got %d", len(info.OwnerReferences))
	}
}

func TestCheckOwnershipArgoCD(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				"argocd.argoproj.io/instance": "prod-app",
			},
		},
	}
	info := CheckOwnership(pod)
	if !info.IsManaged {
		t.Fatal("expected managed by ArgoCD")
	}
	if info.ManagedBy != "argocd" {
		t.Fatalf("expected 'argocd', got %q", info.ManagedBy)
	}
}

func TestCheckOwnershipHelm(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				"meta.helm.sh/release-name": "my-release",
			},
		},
	}
	info := CheckOwnership(pod)
	if !info.IsManaged {
		t.Fatal("expected managed by Helm")
	}
	if info.ManagedBy != "helm" {
		t.Fatalf("expected 'helm', got %q", info.ManagedBy)
	}
}

func TestCheckOwnershipFlux(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				"kustomize.toolkit.fluxcd.io/name": "my-kustomization",
			},
		},
	}
	info := CheckOwnership(pod)
	if !info.IsManaged {
		t.Fatal("expected managed by Flux")
	}
	if info.ManagedBy != "flux" {
		t.Fatalf("expected 'flux', got %q", info.ManagedBy)
	}
}

func TestCheckOwnershipManagedByLabel(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "tekton",
			},
		},
	}
	info := CheckOwnership(pod)
	if !info.IsManaged {
		t.Fatal("expected managed via label")
	}
	if info.ManagedBy != "tekton" {
		t.Fatalf("expected 'tekton', got %q", info.ManagedBy)
	}
}

func TestCheckOwnershipKubectlAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{}`,
			},
		},
	}
	info := CheckOwnership(pod)
	// kubectl annotation warns but does NOT mark as managed.
	if info.IsManaged {
		t.Fatal("kubectl annotation should not mark as managed")
	}
	if len(info.Warnings) == 0 {
		t.Fatal("expected warning for kubectl annotation")
	}
}
