// Package prune provides a document.Transformer that removes documents whose
// trimmed content is below a configurable minimum length.
package prune

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const (
	defaultMinContentLength = 1
	typ                     = "Pruner"
)

// Config holds the pruner configuration.
type Config struct {
	MinContentLength int `validate:"omitempty,gte=1" jsonschema:"description=Minimum runes in trimmed content to keep a document,default=1"`
}

type pruner struct {
	minContentLength int
}

var _ document.Transformer = (*pruner)(nil)

// NewPruner creates a document.Transformer that prunes documents whose trimmed
// content has fewer than MinContentLength runes.
func NewPruner(ctx context.Context, config *Config) (document.Transformer, error) {
	if config == nil {
		config = &Config{}
	}
	if config.MinContentLength <= 0 {
		config.MinContentLength = defaultMinContentLength
	}

	if err := validate.Struct(config); err != nil {
		return nil, err
	}

	return &pruner{
		minContentLength: config.MinContentLength,
	}, nil
}

func (p *pruner) GetType() string {
	return typ
}

func (p *pruner) Transform(ctx context.Context, docs []*schema.Document, opts ...document.TransformerOption) ([]*schema.Document, error) {
	if len(docs) == 0 {
		return docs, nil
	}

	result := make([]*schema.Document, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		if utf8.RuneCountInString(strings.TrimSpace(doc.Content)) >= p.minContentLength {
			result = append(result, doc)
		}
	}
	return result, nil
}
