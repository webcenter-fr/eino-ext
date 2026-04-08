package memory

import "github.com/cloudwego/eino/schema"

type Conversation interface {
	// Append adds a message to the conversation.
	Append(msg *schema.Message)

	// GetFullMessages returns all messages in the conversation.
	GetFullMessages() []*schema.Message

	// GetMessages returns the messages in the conversation. The number of messages returned is limited by the max window size of the conversation.
	GetMessages() []*schema.Message

	// Load loads the conversation from the storage.
	Load() error

	// Save saves the conversation to the storage.
	Save(msg *schema.Message) error
}
