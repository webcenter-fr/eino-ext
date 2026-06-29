package memory

import "github.com/cloudwego/eino/schema"

// TokenCounter returns the estimated number of tokens for a set of messages.
type TokenCounter func(msgs []*schema.Message) int

// DefaultTokenCounter estimates tokens using ~4 characters per token,
// summing over message Content and tool call arguments. No external dependency.
func DefaultTokenCounter(msgs []*schema.Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Arguments)
		}
	}
	// ~4 chars per token
	tokens := total / 4
	if tokens == 0 && total > 0 {
		tokens = 1
	}
	return tokens
}

type Memory interface {
	// GetConversation returns the conversation with the given id. If createIfNotExist is true and the conversation does not exist, it creates a new conversation.
	GetConversation(userId string, id string, createIfNotExist bool) (Conversation, error)

	// ListConversations returns a list of conversation ids for the given user.
	ListConversations(userId string) ([]string, error)

	// DeleteConversation deletes the conversation with the given id.
	DeleteConversation(userId string, id string) error
}
