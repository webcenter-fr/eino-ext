package opensearch_retriever

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type HitFormatter interface {
	FormatHit(doc *schema.Document) string
}

type defaultHitFormatter struct {
	headerFields []HeaderField
}

func NewDefaultHitFormatter(fields []HeaderField) HitFormatter {
	return &defaultHitFormatter{headerFields: fields}
}

func (f *defaultHitFormatter) FormatHit(doc *schema.Document) string {
	var b strings.Builder

	for _, hf := range f.headerFields {
		val, ok := doc.MetaData[hf.MetaKey]
		if !ok {
			continue
		}
		strVal := fmt.Sprintf("%v", val)
		if strVal == "" {
			continue
		}
		label := hf.Label
		if label == "" {
			label = hf.MetaKey
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", label, strVal))
	}

	if doc.Content != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(doc.Content)
	}

	return b.String()
}
