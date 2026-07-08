package chatmodel

import (
	"context"
	"testing"
)

func TestNewEmbeddingNilConfig(t *testing.T) {
	ctx := context.Background()
	_, err := NewEmbedding(ctx, nil)
	if err == nil {
		t.Fatal("NewEmbedding(nil): expected error, got nil")
	}
}

func TestNewEmbeddingUnsupportedPlan(t *testing.T) {
	ctx := context.Background()
	_, err := NewEmbedding(ctx, &EmbeddingConfig{Provider: "gemini"})
	if err == nil {
		t.Fatal("NewEmbedding(unsupported plan): expected error, got nil")
	}
}

func TestNewEmbeddingOpenAI(t *testing.T) {
	ctx := context.Background()
	emb, err := NewEmbedding(ctx, &EmbeddingConfig{
		Provider: OpenAIEmbeddingProvider,
		BaseURL:  "http://localhost:0",
		Model:    "text-embedding-3-small",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewEmbedding(openai): unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("NewEmbedding(openai): nil embedder")
	}
}

func TestNewEmbeddingCopilot(t *testing.T) {
	ctx := context.Background()
	emb, err := NewEmbedding(ctx, &EmbeddingConfig{
		Provider: CopilotEmbeddingProvider,
		BaseURL:  "http://localhost:0",
		Model:    "text-embedding-3-small",
		APIKey:   "test-copilot-token",
	})
	if err != nil {
		t.Fatalf("NewEmbedding(github-copilot): unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("NewEmbedding(github-copilot): nil embedder")
	}
}

func TestNewEmbeddingOllama(t *testing.T) {
	ctx := context.Background()
	_, err := NewEmbedding(ctx, &EmbeddingConfig{
		Provider: OllamaEmbeddingProvider,
		BaseURL:  "http://localhost:0",
		Model:    "nomic-embed-text",
	})
	if err == nil {
		t.Fatal("NewEmbedding(ollama): expected error (not yet implemented), got nil")
	}
}

func TestNewEmbeddingMissingAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("openai", func(t *testing.T) {
		emb, err := NewEmbedding(ctx, &EmbeddingConfig{
			Provider: OpenAIEmbeddingProvider,
			BaseURL:  "http://localhost:0",
			Model:    "text-embedding-3-small",
		})
		if err != nil {
			t.Fatalf("NewEmbedding(openai, no APIKey): unexpected error: %v", err)
		}
		if emb == nil {
			t.Fatal("NewEmbedding(openai, no APIKey): nil embedder")
		}
	})

	t.Run("copilot", func(t *testing.T) {
		_, err := NewEmbedding(ctx, &EmbeddingConfig{
			Provider: CopilotEmbeddingProvider,
			BaseURL:  "http://localhost:0",
			Model:    "text-embedding-3-small",
		})
		if err == nil {
			t.Fatal("NewEmbedding(github-copilot, no APIKey): expected error, got nil")
		}
	})
}

func TestNewEmbeddingDefaultBaseURL(t *testing.T) {
	ctx := context.Background()

	t.Run("openai", func(t *testing.T) {
		emb, err := NewEmbedding(ctx, &EmbeddingConfig{
			Provider: OpenAIEmbeddingProvider,
			Model:    "text-embedding-3-small",
		})
		if err != nil {
			t.Fatalf("NewEmbedding(openai, no BaseURL): unexpected error: %v", err)
		}
		if emb == nil {
			t.Fatal("NewEmbedding(openai, no BaseURL): nil embedder")
		}
	})

	t.Run("copilot", func(t *testing.T) {
		emb, err := NewEmbedding(ctx, &EmbeddingConfig{
			Provider: CopilotEmbeddingProvider,
			Model:    "text-embedding-3-small",
			APIKey:   "test-copilot-token",
		})
		if err != nil {
			t.Fatalf("NewEmbedding(github-copilot, no BaseURL): unexpected error: %v", err)
		}
		if emb == nil {
			t.Fatal("NewEmbedding(github-copilot, no BaseURL): nil embedder")
		}
	})
}
