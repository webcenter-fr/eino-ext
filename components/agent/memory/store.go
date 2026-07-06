package memory

import (
	"context"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// MemoryStore is the composite storage interface for long-term agent memory.
// It composes eino's Indexer and Retriever for RAG compatibility, plus
// maintenance operations.
type MemoryStore interface {
	indexer.Indexer
	retriever.Retriever

	Delete(ctx context.Context, id string) error

	DeleteByFilter(ctx context.Context, filter map[string]any) (deleted int, err error)

	List(ctx context.Context, offset, limit int) ([]*schema.Document, error)

	Count(ctx context.Context) (int, error)
}
