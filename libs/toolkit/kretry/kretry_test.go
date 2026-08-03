package kretry

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	k8sapi "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

type mockNetError struct {
	timeout   bool
	temporary bool
}

func (m *mockNetError) Error() string   { return "mock net error" }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temporary }

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "server timeout", err: k8sapi.NewTimeoutError("timeout", 0), want: true},
		{name: "too many requests", err: k8sapi.NewTooManyRequests("too many", 0), want: true},
		{name: "internal error", err: k8sapi.NewInternalError(errors.New("internal")), want: true},
		{name: "service unavailable", err: k8sapi.NewServiceUnavailable("unavailable"), want: true},
		{name: "not found", err: k8sapi.NewNotFound(schema.GroupResource{}, "test"), want: false},
		{name: "forbidden", err: k8sapi.NewForbidden(schema.GroupResource{}, "test", errors.New("forbidden")), want: false},
		{name: "net timeout", err: &mockNetError{timeout: true}, want: true},
		{name: "net temporary", err: &mockNetError{temporary: true}, want: true},
		{name: "generic error", err: errors.New("generic"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetrySucceedsOnThirdAttempt(t *testing.T) {
	attempts := 0
	err := Retry(context.Background(), func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return k8sapi.NewTimeoutError("timeout", 0)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryPermanentError(t *testing.T) {
	attempts := 0
	err := Retry(context.Background(), func(ctx context.Context) error {
		attempts++
		return errors.New("permanent error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetryContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		return k8sapi.NewTimeoutError("timeout", 0)
	})
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestDoCustomBackoff(t *testing.T) {
	attempts := 0
	backoff := wait.Backoff{
		Steps:    2,
		Duration: 10 * time.Millisecond,
		Factor:   1.0,
	}
	err := Do(context.Background(), backoff, func(ctx context.Context) error {
		attempts++
		if attempts < 2 {
			return &mockNetError{temporary: true}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestIsTransientNetError(t *testing.T) {
	var netErr net.Error = &net.DNSError{IsTimeout: true}
	if !IsTransient(netErr) {
		t.Error("expected DNS timeout to be transient")
	}
}

func TestIsTransientNetErrorTemporary(t *testing.T) {
	var netErr net.Error = &net.DNSError{IsTemporary: true}
	if !IsTransient(netErr) {
		t.Error("expected DNS temporary to be transient")
	}
}
