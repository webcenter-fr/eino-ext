package memory

import (
	"context"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// MemoryStore is the composite storage interface for long-term agent memory.
//
//nolint:revive // MemoryStore is the established public name.
type MemoryStore interface {
	indexer.Indexer
	retriever.Retriever

	Delete(ctx context.Context, id string) error

	DeleteByFilter(ctx context.Context, filter map[string]any) (deleted int, err error)

	List(ctx context.Context, offset, limit int) ([]*schema.Document, error)

	Count(ctx context.Context) (int, error)
}
