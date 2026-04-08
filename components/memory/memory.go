package memory

type Memory interface {
	// GetConversation returns the conversation with the given id. If createIfNotExist is true and the conversation does not exist, it creates a new conversation.
	GetConversation(userId string, id string, createIfNotExist bool) (Conversation, error)

	// ListConversations returns a list of conversation ids for the given user.
	ListConversations(userId string) ([]string, error)

	// DeleteConversation deletes the conversation with the given id.
	DeleteConversation(userId string, id string) error
}
