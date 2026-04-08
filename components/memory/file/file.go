package file

/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/components/memory"
)

// FileMemoryConfig defines the configuration for FileMemory.
type FileMemoryConfig struct {
	Dir           string
	MaxWindowSize int
}

// FileMemory can store messages of each conversation
type FileMemory struct {
	mu            sync.Mutex
	dir           string
	maxWindowSize int
	conversations map[string]map[string]*FileConversation
}

// FileConversation represents a conversation stored in a file.
type FileConversation struct {
	mu sync.Mutex

	UserId   string            `json:"userId"`
	ID       string            `json:"id"`
	Messages []*schema.Message `json:"messages"`

	filePath string

	maxWindowSize int
}

func GetDefaultMemory() memory.Memory {
	return NewFileMemory(FileMemoryConfig{
		MaxWindowSize: 10,
	})
}

func NewFileMemory(cfg FileMemoryConfig) memory.Memory {
	if cfg.Dir == "" {
		cfg.Dir = "/tmp/eino/memory"
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil
	}

	return &FileMemory{
		dir:           cfg.Dir,
		maxWindowSize: cfg.MaxWindowSize,
		conversations: make(map[string]map[string]*FileConversation),
	}
}

func (m *FileMemory) GetConversation(userId string, id string, createIfNotExist bool) (memory.Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.conversations[id]

	filePath := filepath.Join(m.dir, userId, id+".jsonl")
	if !ok {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if createIfNotExist {
				if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
					return nil, errors.Wrap(err, "failed to create file for conversation")
				}
				if _, ok := m.conversations[userId]; !ok {
					m.conversations[userId] = make(map[string]*FileConversation)
				}
				m.conversations[userId][id] = &FileConversation{
					UserId:        userId,
					ID:            id,
					Messages:      make([]*schema.Message, 0),
					filePath:      filePath,
					maxWindowSize: m.maxWindowSize,
				}
			}
		}

		con := &FileConversation{
			UserId:        userId,
			ID:            id,
			Messages:      make([]*schema.Message, 0),
			filePath:      filePath,
			maxWindowSize: m.maxWindowSize,
		}
		con.Load()
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

	delete(m.conversations[userId], id)
	return nil
}

func (c *FileConversation) Append(msg *schema.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Messages = append(c.Messages, msg)

	c.Save(msg)
}

func (c *FileConversation) GetFullMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Messages
}

// get messages with max window size
func (c *FileConversation) GetMessages() []*schema.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.Messages) > c.maxWindowSize {
		return c.Messages[len(c.Messages)-c.maxWindowSize:]
	}

	return c.Messages
}

func (c *FileConversation) Load() error {
	reader, err := os.Open(c.filePath)
	if err != nil {
		return errors.Wrap(err, "failed to open file")
	}
	defer reader.Close()

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

	return nil
}

func (c *FileConversation) Save(msg *schema.Message) error {
	str, _ := json.Marshal(msg)

	// Append to file
	f, err := os.OpenFile(c.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return errors.Wrap(err, "Failed when save message")
	}
	defer f.Close()
	if _, err := f.Write(str); err != nil {
		return errors.Wrap(err, "Failed when save message")
	}
	if _, err := f.WriteString("\n"); err != nil {
		return errors.Wrap(err, "Failed when save message")
	}
	return nil
}
