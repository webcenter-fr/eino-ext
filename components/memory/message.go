package memory

import (
	"github.com/cloudwego/eino/schema"
	"github.com/tmc/langchaingo/llms"
)

type Message struct {
	schema.Message
}

func (m *Message) GetContent() string {
	return m.Content
}

func (m *Message) GetType() llms.ChatMessageType {
	switch m.Role {
	case schema.System:
		return llms.ChatMessageTypeSystem
	case schema.User:
		return llms.ChatMessageTypeHuman
	case schema.Assistant:
		return llms.ChatMessageTypeAI
	case schema.Tool:
		return llms.ChatMessageTypeTool
	default:
		return llms.ChatMessageTypeGeneric
	}
}
