package counter

import "github.com/cloudwego/eino/schema"

// TokenCounter is a function that estimates token count for a conversation.
type TokenCounter func(msgs []*schema.Message) int

// DefaultTokenCounter is a simple heuristic token estimator based on character
// count divided by 4.
func DefaultTokenCounter(msgs []*schema.Message) int {
	total := 0
	for _, msg := range msgs {
		total += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Arguments)
		}
	}
	tokens := total / 4
	if tokens == 0 && total > 0 {
		tokens = 1
	}
	return tokens
}
