package safety

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeAuthorizer struct {
	fn func(ctx context.Context, toolName string, args json.RawMessage) error
}

func (f fakeAuthorizer) AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error {
	return f.fn(ctx, toolName, args)
}

func TestShouldGateWithAuthorization(t *testing.T) {
	writeTools := map[string]bool{"write": true}

	allow := fakeAuthorizer{fn: func(context.Context, string, json.RawMessage) error { return nil }}
	denySentinel := fakeAuthorizer{fn: func(context.Context, string, json.RawMessage) error {
		return ErrExecutionNotAuthorized
	}}
	denyCustom := fakeAuthorizer{fn: func(context.Context, string, json.RawMessage) error {
		return errors.New("denied-by-policy")
	}}

	cases := []struct {
		name     string
		toolName string
		gp       GateParams
		auth     ExecutionAuthorizer
		check    func(t *testing.T, err error)
	}{
		{
			name:     "read tool",
			toolName: "read",
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
			},
		},
		{
			name:     "write dry-run",
			toolName: "write",
			gp:       GateParams{DryRun: true},
			auth:     allow,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
			},
		},
		{
			name:     "write both dryRun+confirmed",
			toolName: "write",
			gp:       GateParams{DryRun: true, Confirmed: true},
			auth:     denySentinel,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected nil error (dry-run precedence), got %v", err)
				}
			},
		},
		{
			name:     "write neither",
			toolName: "write",
			gp:       GateParams{},
			auth:     allow,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrGateRequired) {
					t.Fatalf("expected ErrGateRequired, got %v", err)
				}
			},
		},
		{
			name:     "write confirmed, auth nil",
			toolName: "write",
			gp:       GateParams{Confirmed: true},
			auth:     nil,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrExecutionNotAuthorized) {
					t.Fatalf("expected ErrExecutionNotAuthorized, got %v", err)
				}
			},
		},
		{
			name:     "write confirmed, auth allows",
			toolName: "write",
			gp:       GateParams{Confirmed: true},
			auth:     allow,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
			},
		},
		{
			name:     "write confirmed, auth denies sentinel",
			toolName: "write",
			gp:       GateParams{Confirmed: true},
			auth:     denySentinel,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrExecutionNotAuthorized) {
					t.Fatalf("expected ErrExecutionNotAuthorized, got %v", err)
				}
			},
		},
		{
			name:     "write confirmed, auth denies custom",
			toolName: "write",
			gp:       GateParams{Confirmed: true},
			auth:     denyCustom,
			check: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("expected non-nil error")
				}
				if !strings.Contains(err.Error(), "denied-by-policy") {
					t.Fatalf("expected error to contain 'denied-by-policy', got %v", err)
				}
				if !strings.Contains(err.Error(), "write") {
					t.Fatalf("expected error to contain tool name 'write', got %v", err)
				}
			},
		},
		{
			name:     "unknown tool name",
			toolName: "unknown",
			gp:       GateParams{},
			auth:     denySentinel,
			check: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected nil error (treated read-only), got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ShouldGateWithAuthorization(context.Background(), tc.toolName, writeTools, tc.gp, json.RawMessage(`{}`), tc.auth)
			tc.check(t, err)
		})
	}
}

func TestWithExecutionAuthorizedAndFor(t *testing.T) {
	if ExecutionAuthorizedFor(context.Background(), "t") {
		t.Fatal("expected false before any grant")
	}

	ctx := WithExecutionAuthorized(context.Background(), "t")
	if !ExecutionAuthorizedFor(ctx, "t") {
		t.Fatal("expected true after grant")
	}
	if ExecutionAuthorizedFor(ctx, "other") {
		t.Fatal("expected false for ungranted tool")
	}

	if ExecutionAuthorizedFor(nil, "t") {
		t.Fatal("expected false for nil ctx")
	}
	if WithExecutionAuthorized(nil, "t") != nil {
		t.Fatal("expected nil for nil ctx")
	}

	if ExecutionAuthorizedFor(ctx, "") {
		t.Fatal("expected false for empty toolName")
	}
	if got := WithExecutionAuthorized(ctx, ""); got != ctx {
		t.Fatal("expected ctx unchanged for empty toolName")
	}

	ctx = WithExecutionAuthorized(ctx, "a")
	ctx = WithExecutionAuthorized(ctx, "b")
	if !ExecutionAuthorizedFor(ctx, "a") || !ExecutionAuthorizedFor(ctx, "b") {
		t.Fatal("expected grants to accumulate")
	}
}
