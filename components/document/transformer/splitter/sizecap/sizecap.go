package sizecap

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 200
	typ                 = "SizeCapSplitter"
)

// Config holds the splitter configuration.
type Config struct {
	// ChunkSize is the maximum number of runes per chunk.
	ChunkSize int `validate:"omitempty,gte=1" jsonschema:"description=Maximum runes per chunk,default=1000"`

	// ChunkOverlap is the number of runes to overlap between consecutive chunks.
	ChunkOverlap int `validate:"omitempty,gte=0" jsonschema:"description=Overlap runes between chunks,default=200"`
}

type sizeSplitter struct {
	chunkSize    int
	chunkOverlap int
}

// NewSplitter creates a size-cap document splitter that implements
// document.Transformer.
func NewSplitter(ctx context.Context, config *Config) (document.Transformer, error) {
	if config == nil {
		config = &Config{}
	}
	if config.ChunkSize <= 0 {
		config.ChunkSize = defaultChunkSize
	}
	if config.ChunkOverlap < 0 {
		config.ChunkOverlap = defaultChunkOverlap
	}
	if config.ChunkOverlap >= config.ChunkSize {
		config.ChunkOverlap = 0
	}

	if err := validate.Struct(config); err != nil {
		return nil, err
	}

	return &sizeSplitter{
		chunkSize:    config.ChunkSize,
		chunkOverlap: config.ChunkOverlap,
	}, nil
}

// GetType returns the component type identifier.
func (s *sizeSplitter) GetType() string {
	return typ
}

// Transform splits documents whose content exceeds the configured chunk size
// into smaller overlapping chunks. Short documents pass through unchanged.
func (s *sizeSplitter) Transform(ctx context.Context, docs []*schema.Document, opts ...document.TransformerOption) ([]*schema.Document, error) {
	if len(docs) == 0 {
		return docs, nil
	}

	result := make([]*schema.Document, 0, len(docs))
	for _, doc := range docs {
		chunks := s.splitContent(doc)
		result = append(result, chunks...)
	}
	return result, nil
}

// splitContent splits a single document into chunks. If the content fits within
// chunkSize, it returns the original document as a single-element slice.
func (s *sizeSplitter) splitContent(doc *schema.Document) []*schema.Document {
	content := doc.Content
	if utf8.RuneCountInString(content) <= s.chunkSize {
		return []*schema.Document{doc}
	}

	// Split by paragraphs first, then apply hard splitting on each paragraph.
	paragraphs := s.splitParagraphs(content)

	var chunks []*schema.Document
	for _, para := range paragraphs {
		if para == "" {
			continue
		}
		if utf8.RuneCountInString(para) <= s.chunkSize {
			chunks = append(chunks, s.newChunk(doc, para))
			continue
		}
		hardChunks := s.hardSplit(para)
		for _, hc := range hardChunks {
			chunks = append(chunks, s.newChunk(doc, hc))
		}
	}

	if len(chunks) == 0 {
		return []*schema.Document{doc}
	}

	// Merge overlapping chunks if needed
	if s.chunkOverlap > 0 {
		chunks = s.mergeOverlap(chunks)
	}

	return chunks
}

// splitParagraphs splits text by double newline boundaries.
func (s *sizeSplitter) splitParagraphs(content string) []string {
	return strings.Split(content, "\n\n")
}

// hardSplit splits a string into fixed-size chunks with rune-level precision.
func (s *sizeSplitter) hardSplit(content string) []string {
	var chunks []string
	runes := []rune(content)

	for i := 0; i < len(runes); i += s.chunkSize {
		end := i + s.chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

// mergeOverlap creates overlapping chunks by prepending the tail of the
// previous chunk to the start of the next.
func (s *sizeSplitter) mergeOverlap(chunks []*schema.Document) []*schema.Document {
	if len(chunks) <= 1 {
		return chunks
	}

	result := make([]*schema.Document, 0, len(chunks))

	prevText := ""
	for _, chunk := range chunks {
		text := chunk.Content

		if len(prevText) > 0 {
			prevRunes := []rune(prevText)
			overlapStart := len(prevRunes) - s.chunkOverlap
			if overlapStart < 0 {
				overlapStart = 0
			}
			overlap := string(prevRunes[overlapStart:])
			text = overlap + text
		}

		chunk.Content = text
		result = append(result, chunk)
		prevText = text
	}

	return result
}

// copyMeta copies metadata from the source document into a new map.
func (s *sizeSplitter) copyMeta(doc *schema.Document) map[string]any {
	meta := make(map[string]any, len(doc.MetaData))
	for k, v := range doc.MetaData {
		meta[k] = v
	}
	return meta
}

// newChunk creates a new document from the source's metadata and the given
// content text.
func (s *sizeSplitter) newChunk(source *schema.Document, content string) *schema.Document {
	return &schema.Document{
		ID:       source.ID,
		Content:  content,
		MetaData: s.copyMeta(source),
	}
}
