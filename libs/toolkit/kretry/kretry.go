// Package kretry provides a retry helper for transient Kubernetes API
// server errors. It wraps k8s.io/apimachinery/pkg/util/wait with an
// IsTransient classifier that retries only on server-side timeouts,
// 429, 500, 503, and net.Error timeout/temporary conditions.
package kretry

import (
	"context"
	"errors"
	"net"
	"time"

	k8sapi "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

// DefaultBackoff is the default retry backoff: 3 steps starting at 2s,
// doubling each step, capped at 15s.
var DefaultBackoff = wait.Backoff{
	Steps:    3,
	Duration: 2 * time.Second,
	Factor:   2.0,
	Cap:      15 * time.Second,
}

func Do(ctx context.Context, backoff wait.Backoff, fn func(context.Context) error) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(retryCtx context.Context) (bool, error) {
		err := fn(retryCtx)
		if err == nil {
			return true, nil
		}
		if !IsTransient(err) {
			return false, err
		}
		return false, nil
	})
}

func Retry(ctx context.Context, fn func(context.Context) error) error {
	return Do(ctx, DefaultBackoff, fn)
}

func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if k8sapi.IsServerTimeout(err) {
		return true
	}
	if k8sapi.IsTooManyRequests(err) {
		return true
	}
	if k8sapi.IsInternalError(err) {
		return true
	}
	if k8sapi.IsServiceUnavailable(err) {
		return true
	}
	if k8sapi.IsTimeout(err) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	return false
}
