package opensearch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Config holds the configuration for an OpenSearch-backed memory store.
type Config struct {
	URLs            []string `validate:"required,min=1" jsonschema:"description=OpenSearch cluster URLs"`
	Username        string   `validate:"omitempty" jsonschema:"description=Username for basic authentication"`
	Password        string   `validate:"omitempty" jsonschema:"description=Password for basic authentication"`
	TLSSkipVerify   bool     `validate:"omitempty" jsonschema:"description=Skip TLS certificate verification"`
	IndexName       string   `validate:"omitempty" jsonschema:"description=OpenSearch index name for storing conversations,default=eino_memory"`
	MaxWindowSize   int      `validate:"gte=0" jsonschema:"description=Maximum number of messages to keep in the window"`
	MaxWindowTokens int      `validate:"gte=0" jsonschema:"description=Maximum token budget for GetWindow, 0 means no cap"`

	TokenCounter memory.TokenCounter
}

// OpenSearchMemory stores conversations in an OpenSearch index.
type OpenSearchMemory struct {
	mu              sync.Mutex
	client          opensearchv4.Client
	indexName       string
	maxWindowSize   int
	tokenCounter    memory.TokenCounter
	maxWindowTokens int
	conversations   map[string]map[string]*OpenSearchConversation
}

// OpenSearchConversation represents a conversation stored in OpenSearch.
type OpenSearchConversation struct {
	mu sync.Mutex

	UserID         string
	ConversationID string
	Messages       []*schema.Message
	Activities     []json.RawMessage
	UpdatedAt      string

	client          opensearchv4.Client
	indexName       string
	maxWindowSize   int
	tokenCounter    memory.TokenCounter
	maxWindowTokens int
}

type conversationDoc struct {
	UserID         string            `json:"userId"`
	ConversationID string            `json:"conversationId"`
	Messages       []*schema.Message `json:"messages"`
	Activities     []json.RawMessage `json:"activities"`
	UpdatedAt      string            `json:"updatedAt"`
}

func documentID(userID, conversationID string) string {
	return fmt.Sprintf("%s:%s", userID, conversationID)
}

func (c *OpenSearchConversation) toDoc() conversationDoc {
	return conversationDoc{
		UserID:         c.UserID,
		ConversationID: c.ConversationID,
		Messages:       c.Messages,
		Activities:     c.Activities,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}

func (m *OpenSearchMemory) newConversation(userID, conversationID string, messages []*schema.Message) *OpenSearchConversation {
	return &OpenSearchConversation{
		UserID:          userID,
		ConversationID:  conversationID,
		Messages:        messages,
		client:          m.client,
		indexName:       m.indexName,
		maxWindowSize:   m.maxWindowSize,
		tokenCounter:    m.tokenCounter,
		maxWindowTokens: m.maxWindowTokens,
	}
}

// NewOpenSearchMemory creates a new OpenSearch-backed memory instance. It creates the
// OpenSearch index if it does not already exist.
func NewOpenSearchMemory(cfg Config) (memory.Memory, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, err
	}

	if cfg.IndexName == "" {
		cfg.IndexName = "eino_memory"
	}

	tc := cfg.TokenCounter
	if tc == nil {
		tc = memory.DefaultTokenCounter
	}

	client, err := osclient.New(context.Background(), osclient.Config{
		URLs:          cfg.URLs,
		Username:      cfg.Username,
		Password:      cfg.Password,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}, 0)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := client.Indices().Exists(ctx, []string{cfg.IndexName})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to check if index %s exists", cfg.IndexName)
	}

	if !exists {
		if err := createIndex(ctx, client, cfg.IndexName); err != nil {
			return nil, err
		}
	}

	return &OpenSearchMemory{
		client:          client,
		indexName:       cfg.IndexName,
		maxWindowSize:   cfg.MaxWindowSize,
		tokenCounter:    tc,
		maxWindowTokens: cfg.MaxWindowTokens,
		conversations:   make(map[string]map[string]*OpenSearchConversation),
	}, nil
}

func createIndex(ctx context.Context, client opensearchv4.Client, indexName string) error {
	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":           1,
			"index.auto_expand_replicas": "0-2",
		},
		"mappings": map[string]any{
			"dynamic": false,
			"properties": map[string]any{
				"userId": map[string]any{
					"type": "keyword",
				},
				"conversationId": map[string]any{
					"type": "keyword",
				},
				"messages": map[string]any{
					"type":    "object",
					"dynamic": false,
				},
				"activities": map[string]any{
					"type":    "object",
					"dynamic": false,
				},
				"updatedAt": map[string]any{
					"type": "date",
				},
			},
		},
	}

	_, err := client.Indices().Create(ctx, indexName, body)
	if err != nil {
		return errors.Wrapf(err, "failed to create index %s", indexName)
	}

	return nil
}

// GetConversation returns a conversation by userID and conversationID,
// creating it in OpenSearch if createIfNotExist is true.
func (m *OpenSearchMemory) GetConversation(userID string, conversationID string, createIfNotExist bool) (memory.Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	docID := documentID(userID, conversationID)

	if _, ok := m.conversations[userID]; !ok {
		m.conversations[userID] = make(map[string]*OpenSearchConversation)
	}

	if conv, ok := m.conversations[userID][conversationID]; ok {
		return conv, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := m.client.Document().Get(ctx, &api.GetRequest{
		Index: m.indexName,
		Id:    docID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get conversation document")
	}

	if result.Found {
		var doc conversationDoc
		if err := json.Unmarshal(result.Source, &doc); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal conversation document")
		}

		if doc.Messages == nil {
			doc.Messages = make([]*schema.Message, 0)
		}

		conv := m.newConversation(userID, conversationID, doc.Messages)
		conv.UpdatedAt = doc.UpdatedAt
		if doc.Activities != nil {
			conv.Activities = doc.Activities
		}
		m.conversations[userID][conversationID] = conv
		return conv, nil
	}

	if !createIfNotExist {
		return nil, errors.Errorf("conversation %s not found for user %s", conversationID, userID)
	}

	conv := m.newConversation(userID, conversationID, make([]*schema.Message, 0))

	doc := conv.toDoc()

	_, err = m.client.Document().Create(ctx, &api.CreateRequest{
		Index: m.indexName,
		Id:    docID,
		Body:  doc,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create conversation document")
	}

	m.conversations[userID][conversationID] = conv
	return conv, nil
}

// ListConversations returns all conversation IDs for a user by paginating through OpenSearch results.
func (m *OpenSearchMemory) ListConversations(userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var allIDs []string
	const pageSize = 10000

	for from := 0; ; from += pageSize {
		sr := querydsl.NewSearchRequest().
			Index(m.indexName).
			Query(querydsl.NewTermQuery("userId", userID)).
			FetchSourceIncludeExclude([]string{"conversationId"}, nil).
			From(from).
			Size(pageSize)

		apiReq, err := api.NewSearchRequest(sr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to build search request")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		result, err := m.client.Search().Search(ctx, apiReq)
		cancel()
		if err != nil {
			return nil, errors.Wrap(err, "failed to search conversations")
		}

		for _, hit := range result.Hits.Hits {
			var source struct {
				ConversationID string `json:"conversationId"`
			}
			if err := json.Unmarshal(hit.Source, &source); err != nil {
				return nil, errors.Wrap(err, "failed to unmarshal hit source")
			}
			allIDs = append(allIDs, source.ConversationID)
		}

		if result.Hits.TotalHits == nil || from+len(result.Hits.Hits) >= int(result.Hits.TotalHits.Value) {
			break
		}
	}

	return allIDs, nil
}

// DeleteConversation removes a conversation from the OpenSearch index.
func (m *OpenSearchMemory) DeleteConversation(userID string, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	docID := documentID(userID, conversationID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := m.client.Document().Delete(ctx, &api.DeleteRequest{
		Index: m.indexName,
		Id:    docID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation document")
	}

	if m.conversations[userID] != nil {
		delete(m.conversations[userID], conversationID)
	}

	return nil
}

// Append adds a message to the conversation and immediately persists the full
// conversation document to OpenSearch via an upsert.
func (c *OpenSearchConversation) Append(msg *schema.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Messages = append(c.Messages, msg)

	_ = c.Save(msg)
}

// GetFullMessages returns all messages in the conversation.
func (c *OpenSearchConversation) GetFullMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Messages
}

// GetMessages returns messages bounded by MaxWindowSize (most recent messages).
func (c *OpenSearchConversation) GetMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.maxWindowSize > 0 && len(c.Messages) > c.maxWindowSize {
		return c.Messages[len(c.Messages)-c.maxWindowSize:]
	}

	return c.Messages
}

// AppendSummary adds a summary-marked message to the conversation. It ensures the
// summary marker is set before appending.
func (c *OpenSearchConversation) AppendSummary(summary *schema.Message) {
	if summary.Extra == nil {
		summary.Extra = make(map[string]any)
	}
	summary.Extra[memory.SummaryMarkerKey] = true

	c.Append(summary)
}

// LastSummaryIndex returns the index of the last summary message in Messages, or -1 if none.
func (c *OpenSearchConversation) LastSummaryIndex() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lastSummaryIndexLocked()
}

func (c *OpenSearchConversation) lastSummaryIndexLocked() int {
	return memory.LastSummaryIndex(c.Messages)
}

// GetWindow returns [last summary + following messages], bounded by a token budget.
// If budget <= 0, MaxWindowTokens is used; if that is also 0, no token cap is applied.
//
// Trimming uses binary search (O(log N) calls to tokenCounter) assuming the counter is
// monotonically non-decreasing with more messages. The summary (if present) and the
// last message are always preserved.
func (c *OpenSearchConversation) GetWindow(budget int) []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return memory.SelectWindow(c.Messages, c.tokenCounter, budget, c.maxWindowTokens)
}

// CountTokens counts the tokens in the current window using the injected TokenCounter.
func (c *OpenSearchConversation) CountTokens() int {
	window := c.GetWindow(0)
	return c.tokenCounter(window)
}

// GetActivities returns all stored activity events for the conversation.
func (c *OpenSearchConversation) GetActivities() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Activities
}

// SetActivities replaces all stored activity events for the conversation.
func (c *OpenSearchConversation) SetActivities(raw []json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Activities = raw
	_ = c.Save(nil)
}

// Load loads the conversation from OpenSearch, replacing the in-memory Messages slice.
func (c *OpenSearchConversation) Load() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	docID := documentID(c.UserID, c.ConversationID)

	result, err := c.client.Document().Get(ctx, &api.GetRequest{
		Index: c.indexName,
		Id:    docID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to load conversation document")
	}

	if !result.Found {
		return errors.Errorf("conversation %s not found for user %s", c.ConversationID, c.UserID)
	}

	var doc conversationDoc
	if err := json.Unmarshal(result.Source, &doc); err != nil {
		return errors.Wrap(err, "failed to unmarshal conversation document")
	}

	if doc.Messages == nil {
		doc.Messages = make([]*schema.Message, 0)
	}

	c.Messages = doc.Messages
	c.UpdatedAt = doc.UpdatedAt
	if doc.Activities != nil {
		c.Activities = doc.Activities
	}

	return nil
}

// Save persists the conversation document to OpenSearch via a full-document upsert.
func (c *OpenSearchConversation) Save(msg *schema.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	docID := documentID(c.UserID, c.ConversationID)

	doc := c.toDoc()

	_, err := c.client.Document().Index(ctx, &api.IndexRequest{
		Index: c.indexName,
		Id:    docID,
		Body:  doc,
	})
	if err != nil {
		return errors.Wrap(err, "failed to save conversation document")
	}

	return nil
}

// GetUpdatedAt returns the RFC3339 timestamp of the last update persisted in
// OpenSearch. Returns "" for newly created conversations that have not yet
// been saved.
func (c *OpenSearchConversation) GetUpdatedAt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.UpdatedAt
}
