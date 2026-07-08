package copilot

import (
	"context"
	"testing"
	"time"
)

func TestNewEmbedder(t *testing.T) {
	ctx := context.Background()

	t.Run("valid", func(t *testing.T) {
		e, err := NewEmbedder(ctx, &EmbedderConfig{
			Model: "text-embedding-3-small",
		}, "test-token", "http://localhost:0", 10*time.Second)
		if err != nil {
			t.Fatalf("NewEmbedder: unexpected error: %v", err)
		}
		if e == nil {
			t.Fatal("NewEmbedder: nil embedder")
		}
		if e.model != "text-embedding-3-small" {
			t.Errorf("expected model 'text-embedding-3-small', got %q", e.model)
		}
		if e.token != "test-token" {
			t.Errorf("expected token 'test-token', got %q", e.token)
		}
		if e.baseURL != "http://localhost:0" {
			t.Errorf("expected baseURL 'http://localhost:0', got %q", e.baseURL)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := NewEmbedder(ctx, nil, "token", "http://localhost:0", 10*time.Second)
		if err == nil {
			t.Fatal("NewEmbedder(nil config): expected error, got nil")
		}
	})

	t.Run("empty token", func(t *testing.T) {
		_, err := NewEmbedder(ctx, &EmbedderConfig{
			Model: "text-embedding-3-small",
		}, "", "http://localhost:0", 10*time.Second)
		if err == nil {
			t.Fatal("NewEmbedder(empty token): expected error, got nil")
		}
	})

	t.Run("default baseURL", func(t *testing.T) {
		e, err := NewEmbedder(ctx, &EmbedderConfig{
			Model: "text-embedding-3-small",
		}, "test-token", "", 10*time.Second)
		if err != nil {
			t.Fatalf("NewEmbedder(default baseURL): unexpected error: %v", err)
		}
		if e.baseURL != defaultCopilotBase {
			t.Errorf("expected default baseURL %q, got %q", defaultCopilotBase, e.baseURL)
		}
	})

	t.Run("default timeout", func(t *testing.T) {
		e, err := NewEmbedder(ctx, &EmbedderConfig{
			Model: "text-embedding-3-small",
		}, "test-token", "http://localhost:0", 0)
		if err != nil {
			t.Fatalf("NewEmbedder(default timeout): unexpected error: %v", err)
		}
		if e.httpClient.Timeout != defaultTimeout {
			t.Errorf("expected default timeout %v, got %v", defaultTimeout, e.httpClient.Timeout)
		}
	})

	t.Run("empty model", func(t *testing.T) {
		_, err := NewEmbedder(ctx, &EmbedderConfig{}, "test-token", "http://localhost:0", 10*time.Second)
		if err == nil {
			t.Fatal("NewEmbedder(empty model): expected error, got nil")
		}
	})
}
