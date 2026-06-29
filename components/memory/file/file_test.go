package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/webcenter-fr/eino-ext/components/memory"
)

// helper to build a plain user message
func userMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: content}
}

// helper to build a plain assistant message
func assistantMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

func newConv(t *testing.T, cfg FileMemoryConfig) *FileConversation {
	t.Helper()
	dir := t.TempDir()
	cfg.Dir = dir
	m := NewFileMemory(cfg)
	require.NotNil(t, m)
	conv, err := m.GetConversation("user1", "conv1", true)
	require.NoError(t, err)
	fc, ok := conv.(*FileConversation)
	require.True(t, ok)
	return fc
}

// --- memory.IsSummary / NewSummaryMessage ---

func TestIsSummary(t *testing.T) {
	msg := memory.NewSummaryMessage("this is a summary")
	assert.True(t, memory.IsSummary(msg))
	assert.False(t, memory.IsSummary(userMsg("hello")))
	assert.False(t, memory.IsSummary(nil))
}

func TestNewSummaryMessage(t *testing.T) {
	msg := memory.NewSummaryMessage("summary text")
	assert.Equal(t, schema.Assistant, msg.Role)
	assert.Equal(t, "summary text", msg.Content)
	assert.True(t, memory.IsSummary(msg))
}

// --- DefaultTokenCounter ---

func TestDefaultTokenCounter(t *testing.T) {
	msgs := []*schema.Message{
		{Content: "1234"}, // 4 chars → 1 token
	}
	assert.Equal(t, 1, memory.DefaultTokenCounter(msgs))

	msgs2 := []*schema.Message{
		{Content: "12345678"}, // 8 chars → 2 tokens
	}
	assert.Equal(t, 2, memory.DefaultTokenCounter(msgs2))

	// empty
	assert.Equal(t, 0, memory.DefaultTokenCounter(nil))
}

// --- Round-trip JSONL preserves Extra[SummaryMarkerKey] ---

func TestRoundTripSummaryMarker(t *testing.T) {
	dir := t.TempDir()
	cfg := FileMemoryConfig{Dir: dir}
	m := NewFileMemory(cfg)
	require.NotNil(t, m)

	conv, err := m.GetConversation("u", "c", true)
	require.NoError(t, err)

	// Append a summary
	sum := memory.NewSummaryMessage("round trip summary")
	conv.AppendSummary(sum)

	// Reload from disk by creating a new FileMemory pointing to the same dir
	m2 := NewFileMemory(cfg)
	conv2, err := m2.GetConversation("u", "c", false)
	require.NoError(t, err)

	msgs := conv2.GetFullMessages()
	require.Len(t, msgs, 1)
	assert.True(t, memory.IsSummary(msgs[0]))
	assert.Equal(t, "round trip summary", msgs[0].Content)
}

// --- GetWindow without summary = all messages (or token-bounded) ---

func TestGetWindowNoSummaryNoBudget(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	fc.Append(userMsg("a"))
	fc.Append(assistantMsg("b"))
	fc.Append(userMsg("c"))

	w := fc.GetWindow(0)
	assert.Len(t, w, 3)
}

func TestGetWindowNoSummaryWithBudget(t *testing.T) {
	// Each message is 4 chars = 1 token; budget = 2 tokens → keep last 2 messages.
	fc := newConv(t, FileMemoryConfig{})
	fc.Append(userMsg("aaaa")) // 1 token
	fc.Append(userMsg("bbbb")) // 1 token
	fc.Append(userMsg("cccc")) // 1 token

	w := fc.GetWindow(2) // budget 2 tokens
	assert.LessOrEqual(t, memory.DefaultTokenCounter(w), 2)
	// must keep at least the last message
	assert.Equal(t, "cccc", w[len(w)-1].Content)
}

// --- GetWindow with summary = from last summary ---

func TestGetWindowWithSummary(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	fc.Append(userMsg("old1"))
	fc.Append(userMsg("old2"))

	sum := memory.NewSummaryMessage("summary of old messages")
	fc.AppendSummary(sum)

	fc.Append(userMsg("new1"))
	fc.Append(assistantMsg("new2"))

	w := fc.GetWindow(0)
	// Should start at the summary
	assert.True(t, memory.IsSummary(w[0]))
	assert.Equal(t, "summary of old messages", w[0].Content)
	assert.Len(t, w, 3) // summary + new1 + new2
}

// --- Budget evicts oldest after summary, keeps summary + last user ---

func TestGetWindowBudgetKeepsSummaryAndLastUser(t *testing.T) {
	// Use a custom token counter: 1 token per message for simplicity.
	onePerMsg := memory.TokenCounter(func(msgs []*schema.Message) int {
		return len(msgs)
	})

	dir := t.TempDir()
	cfg := FileMemoryConfig{Dir: dir, TokenCounter: onePerMsg}
	m := NewFileMemory(cfg)
	conv, err := m.GetConversation("u", "c", true)
	require.NoError(t, err)
	fc := conv.(*FileConversation)

	sum := memory.NewSummaryMessage("summary")
	fc.AppendSummary(sum)
	fc.Append(userMsg("msg1"))
	fc.Append(userMsg("msg2"))
	fc.Append(userMsg("msg3"))
	// 4 messages total; budget = 2 → summary + last user

	w := fc.GetWindow(2)
	assert.LessOrEqual(t, len(w), 2)
	// Summary must be present
	assert.True(t, memory.IsSummary(w[0]))
	// Last message must be preserved
	assert.Equal(t, "msg3", w[len(w)-1].Content)
}

// --- Multiple summaries: only the last one is the frontier ---

func TestGetWindowMultipleSummaries(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	fc.Append(userMsg("very old"))
	fc.AppendSummary(memory.NewSummaryMessage("first summary"))
	fc.Append(userMsg("middle"))
	fc.AppendSummary(memory.NewSummaryMessage("second summary"))
	fc.Append(userMsg("recent"))

	w := fc.GetWindow(0)
	// Should start at second summary
	assert.True(t, memory.IsSummary(w[0]))
	assert.Equal(t, "second summary", w[0].Content)
	assert.Len(t, w, 2) // second summary + recent
}

// --- LastSummaryIndex ---

func TestLastSummaryIndex(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	assert.Equal(t, -1, fc.LastSummaryIndex())

	fc.Append(userMsg("a"))
	assert.Equal(t, -1, fc.LastSummaryIndex())

	fc.AppendSummary(memory.NewSummaryMessage("sum1"))
	assert.Equal(t, 1, fc.LastSummaryIndex())

	fc.Append(userMsg("b"))
	fc.AppendSummary(memory.NewSummaryMessage("sum2"))
	assert.Equal(t, 3, fc.LastSummaryIndex())
}

// --- CountTokens ---

func TestCountTokens(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	fc.Append(userMsg("aaaa")) // 4 chars = 1 token
	fc.Append(userMsg("bbbb")) // 4 chars = 1 token

	// No summary, no MaxWindowTokens: window = all messages = 2 tokens
	assert.Equal(t, 2, fc.CountTokens())
}

// --- GetMessages backward compat ---

func TestGetMessagesBackwardCompat(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{MaxWindowSize: 2})
	fc.Append(userMsg("a"))
	fc.Append(userMsg("b"))
	fc.Append(userMsg("c"))

	msgs := fc.GetMessages()
	assert.Len(t, msgs, 2)
	assert.Equal(t, "b", msgs[0].Content)
	assert.Equal(t, "c", msgs[1].Content)
}

// --- AppendSummary ensures marker even if caller forgot ---

func TestAppendSummaryEnsuresMarker(t *testing.T) {
	fc := newConv(t, FileMemoryConfig{})
	// Pass a message without the marker
	bare := &schema.Message{Role: schema.Assistant, Content: "bare summary"}
	fc.AppendSummary(bare)

	msgs := fc.GetFullMessages()
	require.Len(t, msgs, 1)
	assert.True(t, memory.IsSummary(msgs[0]))
}

// --- MaxWindowTokens from config is used when GetWindow called with budget=0 ---

func TestGetWindowUsesMaxWindowTokensFromConfig(t *testing.T) {
	onePerMsg := memory.TokenCounter(func(msgs []*schema.Message) int {
		return len(msgs)
	})

	dir := t.TempDir()
	cfg := FileMemoryConfig{Dir: dir, TokenCounter: onePerMsg, MaxWindowTokens: 2}
	m := NewFileMemory(cfg)
	conv, err := m.GetConversation("u", "c", true)
	require.NoError(t, err)
	fc := conv.(*FileConversation)

	sum := memory.NewSummaryMessage("sum")
	fc.AppendSummary(sum)
	fc.Append(userMsg("m1"))
	fc.Append(userMsg("m2"))
	fc.Append(userMsg("m3"))

	// GetWindow(0) should use MaxWindowTokens=2
	w := fc.GetWindow(0)
	assert.LessOrEqual(t, len(w), 2)
	// Summary must survive
	assert.True(t, memory.IsSummary(w[0]))
}

// --- filepath used by GetConversation when file exists but not in cache ---

func TestGetConversationLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := FileMemoryConfig{Dir: dir}

	// First instance: write some messages
	m1 := NewFileMemory(cfg)
	conv1, err := m1.GetConversation("u", "c", true)
	require.NoError(t, err)
	conv1.AppendSummary(memory.NewSummaryMessage("persisted summary"))
	conv1.Append(userMsg("hello"))

	// Second instance: load from disk
	m2 := NewFileMemory(cfg)
	conv2, err := m2.GetConversation("u", "c", false)
	require.NoError(t, err)

	msgs := conv2.GetFullMessages()
	require.Len(t, msgs, 2)
	assert.True(t, memory.IsSummary(msgs[0]))
	assert.Equal(t, "hello", msgs[1].Content)
}

// --- DeleteConversation removes the file ---

func TestDeleteConversation(t *testing.T) {
	dir := t.TempDir()
	cfg := FileMemoryConfig{Dir: dir}
	m := NewFileMemory(cfg)

	_, err := m.GetConversation("u", "c", true)
	require.NoError(t, err)

	err = m.DeleteConversation("u", "c")
	require.NoError(t, err)

	filePath := filepath.Join(dir, "u", "c.jsonl")
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr))
}
