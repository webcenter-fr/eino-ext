package counter

import "github.com/cloudwego/eino/schema"

type TokenCounter func(msgs []*schema.Message) int

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
