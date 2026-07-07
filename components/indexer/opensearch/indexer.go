// Package opensearch provides an Indexer implementation backed by OpenSearch,
// plus supporting utilities for index lifecycle management (field mapping
// merges, source-hash lookups, bulk reconciliation).
package opensearch

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

const (
	indexerType         = "OpenSearch"
	defaultBatchSize    = 100
	defaultVectorField  = "vector"
	defaultContentField = "content"
)

// DocumentToFields maps an Eino document to the raw OpenSearch fields that
// will be stored for it. Implementations are free to project any subset of
// [schema.Document.MetaData] into the returned map; callers must not rely on
// the ContentField/VectorField being populated automatically when a custom
// mapper is supplied.
type DocumentToFields func(ctx context.Context, doc *schema.Document) (map[string]any, error)

// Config configures the OpenSearch indexer.
type Config struct {
	// URLs is the list of OpenSearch cluster URLs.
	URLs []string `validate:"required,min=1,dive,required" jsonschema:"description=OpenSearch cluster URLs"`

	// Username for basic authentication.
	Username string `validate:"omitempty" jsonschema:"description=Username for basic authentication"`

	// Password for basic authentication.
	Password string `validate:"omitempty" jsonschema:"description=Password for basic authentication"`

	// TLSSkipVerify controls whether TLS certificate verification is skipped.
	TLSSkipVerify bool `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`

	// Index is the default OpenSearch index documents are stored into.
	// Can be overridden per call with indexer.WithIndex.
	Index string `validate:"required" jsonschema:"description=Default OpenSearch index name"`

	// BatchSize is the maximum number of documents embedded and bulk-indexed
	// per request. Defaults to 100.
	BatchSize int `validate:"omitempty,gte=1" jsonschema:"description=Max documents per bulk request,default=100"`

	// DocumentToFields customizes how a document is projected into OpenSearch
	// fields. If nil, defaultDocumentToFields is used: it stores the document
	// content under ContentField and every MetaData entry as-is, plus the
	// vector (from Embedding or schema.Document.DenseVector) under
	// VectorField when embedding is enabled.
	DocumentToFields DocumentToFields `validate:"-" jsonschema:"-"`

	// Embedding, when set, vectorizes doc.Content and stores the resulting
	// vector under VectorField. If a document already carries a vector via
	// [schema.Document.WithDenseVector], that vector is used instead and no
	// embedding call is made for it.
	Embedding embedding.Embedder `validate:"-" jsonschema:"-"`

	// VectorField is the knn_vector field that stores the embedded content.
	// Defaults to "vector".
	VectorField string `validate:"omitempty" jsonschema:"description=knn_vector field name,default=vector"`

	// ContentField is the field that stores doc.Content when using the
	// default field mapper. Defaults to "content".
	ContentField string `validate:"omitempty" jsonschema:"description=Text field for document content,default=content"`
}

// Indexer implements indexer.Indexer backed by OpenSearch.
type Indexer struct {
	client opensearchv4.Client
	config Config
}

// Compile-time check that Indexer implements indexer.Indexer.
var _ indexer.Indexer = (*Indexer)(nil)

// NewIndexer creates a new OpenSearch indexer.
func NewIndexer(ctx context.Context, config *Config) (*Indexer, error) {
	if config == nil {
		config = &Config{}
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.VectorField == "" {
		config.VectorField = defaultVectorField
	}
	if config.ContentField == "" {
		config.ContentField = defaultContentField
	}
	if err := validate.Struct(config); err != nil {
		return nil, err
	}

	client, err := osclient.New(osclient.Config{
		URLs:          config.URLs,
		Username:      config.Username,
		Password:      config.Password,
		TLSSkipVerify: config.TLSSkipVerify,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}

	mapper := config.DocumentToFields
	if mapper == nil {
		mapper = defaultDocumentToFields(config.ContentField)
	}
	configCopy := *config
	configCopy.DocumentToFields = mapper

	return &Indexer{
		client: client,
		config: configCopy,
	}, nil
}

// GetType returns the component type identifier.
func (i *Indexer) GetType() string {
	return indexerType
}

// IsCallbacksEnabled reports that this indexer supports callbacks.
func (i *Indexer) IsCallbacksEnabled() bool {
	return true
}

// Client returns the underlying OpenSearch client.
func (i *Indexer) Client() opensearchv4.Client {
	return i.client
}

// Store embeds (when configured) and bulk-indexes the given documents,
// returning their IDs. Documents without an ID are assigned one via
// [schema.Document.ID]; the caller is expected to set it upstream, otherwise
// OpenSearch auto-generates the document ID and Store returns an empty string
// for that position.
func (i *Indexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) (ids []string, err error) {
	ctx = callbacks.EnsureRunInfo(ctx, i.GetType(), components.ComponentOfIndexer)
	ctx = callbacks.OnStart(ctx, &indexer.CallbackInput{Docs: docs})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	options := indexer.GetCommonOptions(&indexer.Options{
		Index:     &i.config.Index,
		Embedding: i.config.Embedding,
	}, opts...)

	targetIndex := i.config.Index
	if options.Index != nil && *options.Index != "" {
		targetIndex = *options.Index
	}

	ctx, cancel := ensureContextTimeout(ctx, 30*time.Second)
	defer cancel()

	ids, err = i.bulkStore(ctx, targetIndex, docs, options)
	if err != nil {
		return nil, err
	}

	callbacks.OnEnd(ctx, &indexer.CallbackOutput{IDs: ids})

	return ids, nil
}

// bulkStore embeds documents in batches of config.BatchSize and writes them
// to targetIndex via the OpenSearch bulk API.
func (i *Indexer) bulkStore(ctx context.Context, targetIndex string, docs []*schema.Document, options *indexer.Options) ([]string, error) {
	ids := make([]string, 0, len(docs))

	for start := 0; start < len(docs); start += i.config.BatchSize {
		end := start + i.config.BatchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[start:end]

		batchIDs, err := i.storeBatch(ctx, targetIndex, batch, options)
		if err != nil {
			return nil, err
		}
		ids = append(ids, batchIDs...)
	}

	return ids, nil
}

// storeBatch embeds a single batch (when Embedding is configured) and sends
// it to OpenSearch via one bulk request.
func (i *Indexer) storeBatch(ctx context.Context, targetIndex string, batch []*schema.Document, options *indexer.Options) ([]string, error) {
	for _, doc := range batch {
		if doc == nil {
			return nil, errors.New("cannot index a nil document")
		}
	}
	vectors, err := i.embedBatch(ctx, batch, options.Embedding)
	if err != nil {
		return nil, err
	}

	var buf strings.Builder
	ids := make([]string, len(batch))

	for idx, doc := range batch {
		fields, err := i.config.DocumentToFields(ctx, doc)
		if err != nil {
			return nil, errors.Wrap(err, "failed to map document to OpenSearch fields")
		}
		if fields == nil {
			fields = make(map[string]any)
		}

		if vectors != nil && vectors[idx] != nil {
			fields[i.config.VectorField] = vectors[idx]
		}

		meta := map[string]any{"_index": targetIndex}
		if doc.ID != "" {
			meta["_id"] = doc.ID
		}

		if err := writeBulkLine(&buf, map[string]any{"index": meta}, fields); err != nil {
			return nil, err
		}

		ids[idx] = doc.ID
	}

	if buf.Len() == 0 {
		return ids, nil
	}

	resp, err := i.client.Document().Bulk(ctx, targetIndex, buf.String())
	if err != nil {
		return nil, errors.Wrap(err, "failed to bulk index documents into OpenSearch")
	}

	if resp.Errors {
		return applyBulkResults(ids, resp), bulkError(resp)
	}

	return applyBulkResults(ids, resp), nil
}

// embedBatch returns one vector per document in batch, using an already
// present dense vector when set, and calling emb.EmbedStrings for documents
// that lack one and emb is configured. Documents that lack a vector and have
// no Embedder configured are simply left without one (plain BM25 indexing).
// It returns nil when no document ends up with a vector at all.
func (i *Indexer) embedBatch(ctx context.Context, batch []*schema.Document, emb embedding.Embedder) ([][]float64, error) {
	vectors := make([][]float64, len(batch))

	var (
		pending    []string
		pendingIdx []int
		hasVector  bool
	)

	for idx, doc := range batch {
		if v := doc.DenseVector(); v != nil {
			vectors[idx] = v
			hasVector = true
			continue
		}
		if emb != nil {
			pending = append(pending, doc.Content)
			pendingIdx = append(pendingIdx, idx)
		}
	}

	if len(pending) == 0 {
		if !hasVector {
			return nil, nil
		}
		return vectors, nil
	}

	embedded, err := emb.EmbedStrings(i.embeddingCtx(ctx, emb), pending)
	if err != nil {
		return nil, errors.Wrap(err, "failed to embed documents")
	}
	if len(embedded) != len(pending) {
		return nil, errors.Errorf("embedding returned %d vectors, expected %d", len(embedded), len(pending))
	}

	for pos, idx := range pendingIdx {
		vectors[idx] = embedded[pos]
	}

	return vectors, nil
}

// embeddingCtx wires up callback run info so the embedder's own callbacks
// (if any) are correctly attributed as a nested embedding component call.
func (i *Indexer) embeddingCtx(ctx context.Context, emb embedding.Embedder) context.Context {
	runInfo := &callbacks.RunInfo{
		Component: components.ComponentOfEmbedding,
	}
	if embType, ok := components.GetType(emb); ok {
		runInfo.Type = embType
	}
	runInfo.Name = runInfo.Type + string(runInfo.Component)

	return callbacks.ReuseHandlers(ctx, runInfo)
}

// writeBulkLine appends one action-line + one source-line (both newline
// terminated) to buf, per the OpenSearch/Elasticsearch bulk NDJSON format.
func writeBulkLine(buf *strings.Builder, action map[string]any, source map[string]any) error {
	actionBytes, err := json.Marshal(action)
	if err != nil {
		return errors.Wrap(err, "failed to marshal bulk action line")
	}
	sourceBytes, err := json.Marshal(source)
	if err != nil {
		return errors.Wrap(err, "failed to marshal bulk source line")
	}
	buf.Write(actionBytes)
	buf.WriteByte('\n')
	buf.Write(sourceBytes)
	buf.WriteByte('\n')
	return nil
}

// applyBulkResults fills in server-generated IDs (when the caller did not
// set doc.ID) from the bulk response, preserving input order.
func applyBulkResults(ids []string, resp *api.BulkResponse) []string {
	for pos, item := range resp.Items {
		if pos >= len(ids) {
			break
		}
		if ids[pos] != "" {
			continue
		}
		result, ok := item["index"]
		if !ok || result == nil {
			continue
		}
		ids[pos] = result.Id
	}
	return ids
}

// bulkError builds an aggregate error describing every failed item in a bulk
// response.
func bulkError(resp *api.BulkResponse) error {
	var msgs []string
	for pos, item := range resp.Items {
		result, ok := item["index"]
		if !ok || result == nil || result.Error == nil {
			continue
		}
		msgs = append(msgs, errors.Errorf("item %d (id=%q): %s: %s", pos, result.Id, result.Error.Type, result.Error.Reason).Error())
	}
	if len(msgs) == 0 {
		return errors.New("bulk index request completed with errors")
	}
	return errors.Errorf("bulk index request completed with %d failed item(s): %s", len(msgs), strings.Join(msgs, "; "))
}

// defaultDocumentToFields returns a DocumentToFields mapper that stores
// doc.Content under contentField and copies every MetaData entry verbatim
// (skipping the internal eino score/sub-index/vector keys, which are not
// meaningful as stored fields).
func defaultDocumentToFields(contentField string) DocumentToFields {
	return func(_ context.Context, doc *schema.Document) (map[string]any, error) {
		fields := make(map[string]any, len(doc.MetaData)+1)
		fields[contentField] = doc.Content
		for k, v := range doc.MetaData {
			if strings.HasPrefix(k, "_") {
				continue
			}
			fields[k] = v
		}
		return fields, nil
	}
}

// ensureContextTimeout returns ctx unchanged if it already carries a
// deadline, otherwise wraps it with a sane default timeout for OpenSearch
// bulk requests.
func ensureContextTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}
