package file

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// FileMemoryConfig defines the configuration for FileMemory.
type FileMemoryConfig struct {
	Dir           string `validate:"omitempty" jsonschema:"description=Directory path for storing memory files,default=/tmp/eino/memory"`
	MaxWindowSize int    `validate:"gte=0" jsonschema:"description=Maximum number of messages to keep in the window"`

	// TokenCounter is the function used to count tokens. Defaults to memory.DefaultTokenCounter.
	TokenCounter memory.TokenCounter

	// MaxWindowTokens is the maximum token budget for GetWindow. 0 means no token cap.
	MaxWindowTokens int `validate:"gte=0" jsonschema:"description=Maximum token budget for GetWindow, 0 means no cap"`
}

// FileMemory can store messages of each conversation
type FileMemory struct {
	mu              sync.Mutex
	dir             string
	maxWindowSize   int
	tokenCounter    memory.TokenCounter
	maxWindowTokens int
	conversations   map[string]map[string]*FileConversation
}

// FileConversation represents a conversation stored in a file.
type FileConversation struct {
	mu sync.Mutex

	UserId     string            `json:"userId"`
	ID         string            `json:"id"`
	Messages   []*schema.Message `json:"messages"`
	Activities []json.RawMessage `json:"-"`

	filePath string

	maxWindowSize   int
	tokenCounter    memory.TokenCounter
	maxWindowTokens int
}

func GetDefaultMemory() (memory.Memory, error) {
	return NewFileMemory(FileMemoryConfig{
		MaxWindowSize: 10,
	})
}

func NewFileMemory(cfg FileMemoryConfig) (memory.Memory, error) {
	if cfg.Dir == "" {
		cfg.Dir = "/tmp/eino/memory"
	}
	if err := validate.Struct(&cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, errors.Wrap(err, "failed to create memory directory")
	}

	tc := cfg.TokenCounter
	if tc == nil {
		tc = memory.DefaultTokenCounter
	}

	return &FileMemory{
		dir:             cfg.Dir,
		maxWindowSize:   cfg.MaxWindowSize,
		tokenCounter:    tc,
		maxWindowTokens: cfg.MaxWindowTokens,
		conversations:   make(map[string]map[string]*FileConversation),
	}, nil
}

func (m *FileMemory) GetConversation(userId string, id string, createIfNotExist bool) (memory.Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := filepath.Join(m.dir, userId, id+".jsonl")

	_, ok := m.conversations[userId]
	if !ok {
		if _, err := os.Stat(filepath.Dir(filePath)); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
				return nil, errors.Wrap(err, "failed to create directory for conversation")
			}
		}
		m.conversations[userId] = make(map[string]*FileConversation)
	}

	_, ok = m.conversations[userId][id]
	if !ok {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if createIfNotExist {
				if err := os.WriteFile(filePath, []byte(""), 0o600); err != nil {
					return nil, errors.Wrap(err, "failed to create file for conversation")
				}
				if _, ok := m.conversations[userId]; !ok {
					m.conversations[userId] = make(map[string]*FileConversation)
				}
				m.conversations[userId][id] = &FileConversation{
					UserId:          userId,
					ID:              id,
					Messages:        make([]*schema.Message, 0),
					filePath:        filePath,
					maxWindowSize:   m.maxWindowSize,
					tokenCounter:    m.tokenCounter,
					maxWindowTokens: m.maxWindowTokens,
				}
			}
		}

		con := &FileConversation{
			UserId:          userId,
			ID:              id,
			Messages:        make([]*schema.Message, 0),
			filePath:        filePath,
			maxWindowSize:   m.maxWindowSize,
			tokenCounter:    m.tokenCounter,
			maxWindowTokens: m.maxWindowTokens,
		}
		_ = con.Load()
		m.conversations[userId][id] = con
	}

	return m.conversations[userId][id], nil
}

func (m *FileMemory) ListConversations(userId string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	files, err := os.ReadDir(filepath.Join(m.dir, userId))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read directory")
	}

	ids := make([]string, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		ids = append(ids, strings.TrimSuffix(file.Name(), ".jsonl"))
	}

	return ids, nil
}

func (m *FileMemory) DeleteConversation(userId string, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := filepath.Join(m.dir, userId, id+".jsonl")
	if err := os.Remove(filePath); err != nil {
		return errors.Wrap(err, "failed to delete file")
	}
	_ = os.Remove(filePath + ".activities")

	delete(m.conversations[userId], id)
	return nil
}

func (c *FileConversation) Append(msg *schema.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Messages = append(c.Messages, msg)

	_ = c.Save(msg)
}

func (c *FileConversation) GetFullMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Messages
}

// GetMessages returns messages bounded by MaxWindowSize (backward-compatible).
func (c *FileConversation) GetMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.maxWindowSize > 0 && len(c.Messages) > c.maxWindowSize {
		return c.Messages[len(c.Messages)-c.maxWindowSize:]
	}

	return c.Messages
}

// AppendSummary adds a summary-marked message to the conversation (non-destructive).
// It ensures the summary marker is set before appending.
func (c *FileConversation) AppendSummary(summary *schema.Message) {
	// Ensure the marker is present
	if summary.Extra == nil {
		summary.Extra = make(map[string]any)
	}
	summary.Extra[memory.SummaryMarkerKey] = true

	c.Append(summary)
}

// LastSummaryIndex returns the index of the last summary message in Messages, or -1.
func (c *FileConversation) LastSummaryIndex() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lastSummaryIndexLocked()
}

// lastSummaryIndexLocked is the non-locking version for internal use.
func (c *FileConversation) lastSummaryIndexLocked() int {
	return memory.LastSummaryIndex(c.Messages)
}

// GetWindow returns [last summary + following messages], bounded by a token budget.
// If budget <= 0, MaxWindowTokens is used; if that is also 0, no token cap is applied.
//
// Trimming uses binary search (O(log N) calls to tokenCounter) assuming the counter is
// monotonically non-decreasing with more messages, which holds for any additive counter.
// The summary (if present) and the last message are always preserved.
func (c *FileConversation) GetWindow(budget int) []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return memory.SelectWindow(c.Messages, c.tokenCounter, budget, c.maxWindowTokens)
}

// CountTokens counts the tokens of the current window using the injected TokenCounter.
func (c *FileConversation) CountTokens() int {
	window := c.GetWindow(0)
	return c.tokenCounter(window)
}

func (c *FileConversation) GetActivities() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Activities
}

func (c *FileConversation) SetActivities(raw []json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Activities = raw
	c.saveActivities()
}

func (c *FileConversation) activitiesPath() string {
	return filepath.Dir(c.filePath) + "/" + filepath.Base(c.filePath) + ".activities"
}

func (c *FileConversation) saveActivities() {
	data, err := json.Marshal(c.Activities)
	if err != nil {
		return
	}
	_ = os.WriteFile(c.activitiesPath(), data, 0o600)
}

func (c *FileConversation) Load() error {
	reader, err := os.Open(c.filePath)
	if err != nil {
		return errors.Wrap(err, "failed to open file")
	}
	defer func() { _ = reader.Close() }()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		var msg schema.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return errors.Wrap(err, "failed to unmarshal message")
		}
		c.Messages = append(c.Messages, &msg)
	}

	if err := scanner.Err(); err != nil {
		return errors.Wrap(err, "scanner error")
	}

	data, err := os.ReadFile(c.activitiesPath())
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to read activities file")
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &c.Activities); err != nil {
			return errors.Wrap(err, "failed to unmarshal activities")
		}
	}

	return nil
}

func (c *FileConversation) Save(msg *schema.Message) error {
	str, _ := json.Marshal(msg)

	// Append to file
	f, err := os.OpenFile(c.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Wrap(err, "failed to open message file")
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(str); err != nil {
		return errors.Wrap(err, "failed to write message")
	}
	if _, err := f.WriteString("\n"); err != nil {
		return errors.Wrap(err, "failed to write message newline")
	}
	return nil
}

// GetUpdatedAt returns the RFC3339 modification time of the backing .jsonl
// file. Returns "" when the file cannot be stat'd.
func (c *FileConversation) GetUpdatedAt() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	info, err := os.Stat(c.filePath)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
