package opensearch

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/webcenter-fr/eino-ext/components/memory"
)

func userMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: content}
}

func assistantMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

func newConv(t *testing.T, cfg Config) *OpenSearchConversation {
	t.Helper()
	tc := cfg.TokenCounter
	if tc == nil {
		tc = memory.DefaultTokenCounter
	}
	return &OpenSearchConversation{
		UserID:          "user1",
		ConversationID:  "conv1",
		Messages:        make([]*schema.Message, 0),
		client:          nil,
		indexName:       "test",
		maxWindowSize:   cfg.MaxWindowSize,
		tokenCounter:    tc,
		maxWindowTokens: cfg.MaxWindowTokens,
	}
}

func appendMsg(t *testing.T, conv *OpenSearchConversation, msg *schema.Message) {
	t.Helper()
	conv.mu.Lock()
	defer conv.mu.Unlock()
	conv.Messages = append(conv.Messages, msg)
}

func appendSummary(t *testing.T, conv *OpenSearchConversation, summary *schema.Message) {
	t.Helper()
	if summary.Extra == nil {
		summary.Extra = make(map[string]any)
	}
	summary.Extra[memory.SummaryMarkerKey] = true
	appendMsg(t, conv, summary)
}

func TestGetWindowNoSummaryNoBudget(t *testing.T) {
	fc := newConv(t, Config{})
	appendMsg(t, fc, userMsg("a"))
	appendMsg(t, fc, assistantMsg("b"))
	appendMsg(t, fc, userMsg("c"))

	w := fc.GetWindow(0)
	assert.Len(t, w, 3)
}

func TestGetWindowNoSummaryWithBudget(t *testing.T) {
	fc := newConv(t, Config{})
	appendMsg(t, fc, userMsg("aaaa"))
	appendMsg(t, fc, userMsg("bbbb"))
	appendMsg(t, fc, userMsg("cccc"))

	w := fc.GetWindow(2)
	assert.LessOrEqual(t, memory.DefaultTokenCounter(w), 2)
	assert.Equal(t, "cccc", w[len(w)-1].Content)
}

func TestGetWindowWithSummary(t *testing.T) {
	fc := newConv(t, Config{})
	appendMsg(t, fc, userMsg("old1"))
	appendMsg(t, fc, userMsg("old2"))

	sum := memory.NewSummaryMessage("summary of old messages")
	appendSummary(t, fc, sum)

	appendMsg(t, fc, userMsg("new1"))
	appendMsg(t, fc, assistantMsg("new2"))

	w := fc.GetWindow(0)
	assert.True(t, memory.IsSummary(w[0]))
	assert.Equal(t, "summary of old messages", w[0].Content)
	assert.Len(t, w, 3)
}

func TestGetWindowBudgetKeepsSummaryAndLastUser(t *testing.T) {
	onePerMsg := memory.TokenCounter(func(msgs []*schema.Message) int {
		return len(msgs)
	})

	fc := newConv(t, Config{TokenCounter: onePerMsg})

	sum := memory.NewSummaryMessage("summary")
	appendSummary(t, fc, sum)
	appendMsg(t, fc, userMsg("msg1"))
	appendMsg(t, fc, userMsg("msg2"))
	appendMsg(t, fc, userMsg("msg3"))

	w := fc.GetWindow(2)
	assert.LessOrEqual(t, len(w), 2)
	assert.True(t, memory.IsSummary(w[0]))
	assert.Equal(t, "msg3", w[len(w)-1].Content)
}

func TestGetWindowMultipleSummaries(t *testing.T) {
	fc := newConv(t, Config{})
	appendMsg(t, fc, userMsg("very old"))
	appendSummary(t, fc, memory.NewSummaryMessage("first summary"))
	appendMsg(t, fc, userMsg("middle"))
	appendSummary(t, fc, memory.NewSummaryMessage("second summary"))
	appendMsg(t, fc, userMsg("recent"))

	w := fc.GetWindow(0)
	assert.True(t, memory.IsSummary(w[0]))
	assert.Equal(t, "second summary", w[0].Content)
	assert.Len(t, w, 2)
}

func TestLastSummaryIndex(t *testing.T) {
	fc := newConv(t, Config{})
	assert.Equal(t, -1, fc.LastSummaryIndex())

	appendMsg(t, fc, userMsg("a"))
	assert.Equal(t, -1, fc.LastSummaryIndex())

	appendSummary(t, fc, memory.NewSummaryMessage("sum1"))
	assert.Equal(t, 1, fc.LastSummaryIndex())

	appendMsg(t, fc, userMsg("b"))
	appendSummary(t, fc, memory.NewSummaryMessage("sum2"))
	assert.Equal(t, 3, fc.LastSummaryIndex())
}

func TestCountTokens(t *testing.T) {
	fc := newConv(t, Config{})
	appendMsg(t, fc, userMsg("aaaa"))
	appendMsg(t, fc, userMsg("bbbb"))

	assert.Equal(t, 2, fc.CountTokens())
}

func TestGetMessagesBackwardCompat(t *testing.T) {
	fc := newConv(t, Config{MaxWindowSize: 2})
	appendMsg(t, fc, userMsg("a"))
	appendMsg(t, fc, userMsg("b"))
	appendMsg(t, fc, userMsg("c"))

	msgs := fc.GetMessages()
	assert.Len(t, msgs, 2)
	assert.Equal(t, "b", msgs[0].Content)
	assert.Equal(t, "c", msgs[1].Content)
}

func TestAppendSummaryEnsuresMarker(t *testing.T) {
	fc := newConv(t, Config{})
	bare := &schema.Message{Role: schema.Assistant, Content: "bare summary"}
	appendSummary(t, fc, bare)

	msgs := fc.GetFullMessages()
	require.Len(t, msgs, 1)
	assert.True(t, memory.IsSummary(msgs[0]))
}

func TestGetWindowUsesMaxWindowTokensFromConfig(t *testing.T) {
	onePerMsg := memory.TokenCounter(func(msgs []*schema.Message) int {
		return len(msgs)
	})

	fc := newConv(t, Config{TokenCounter: onePerMsg, MaxWindowTokens: 2})

	sum := memory.NewSummaryMessage("sum")
	appendSummary(t, fc, sum)
	appendMsg(t, fc, userMsg("m1"))
	appendMsg(t, fc, userMsg("m2"))
	appendMsg(t, fc, userMsg("m3"))

	w := fc.GetWindow(0)
	assert.LessOrEqual(t, len(w), 2)
	assert.True(t, memory.IsSummary(w[0]))
}

func TestDocumentID(t *testing.T) {
	assert.Equal(t, "user1:conv1", documentID("user1", "conv1"))
	assert.Equal(t, "user:with:colons:conv-id", documentID("user:with:colons", "conv-id"))
}

func TestConversationDocSerialization(t *testing.T) {
	msg := userMsg("hello")
	doc := conversationDoc{
		UserID:         "user1",
		ConversationID: "conv1",
		Messages:       []*schema.Message{msg},
		UpdatedAt:      "2026-01-01T00:00:00Z",
	}

	data, err := json.Marshal(doc)
	require.NoError(t, err)

	var restored conversationDoc
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, doc.UserID, restored.UserID)
	assert.Equal(t, doc.ConversationID, restored.ConversationID)
	require.Len(t, restored.Messages, 1)
	assert.Equal(t, "hello", restored.Messages[0].Content)
	assert.Equal(t, schema.User, restored.Messages[0].Role)
}

func TestGetUpdatedAt_EmptyForNewConversation(t *testing.T) {
	fc := newConv(t, Config{})
	assert.Equal(t, "", fc.GetUpdatedAt())
}

func TestGetUpdatedAt_AfterToDoc(t *testing.T) {
	fc := newConv(t, Config{})
	doc := fc.toDoc()
	// toDoc stamps time.Now() in RFC3339; verify it parses back.
	assert.NotEmpty(t, doc.UpdatedAt)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, doc.UpdatedAt)
}
