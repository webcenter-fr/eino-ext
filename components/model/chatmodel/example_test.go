package chatmodel_test

import (
	"context"
	"fmt"

	"github.com/webcenter-fr/eino-ext/components/model/chatmodel"
)

// ExampleParseThinkingLevel shows how user-facing strings map onto the
// provider-generic thinking levels.
func ExampleParseThinkingLevel() {
	for _, s := range []string{"", "true", "low", "High"} {
		lvl, _ := chatmodel.ParseThinkingLevel(s)
		fmt.Printf("%q -> %s\n", s, lvl)
	}
	// Output:
	// "" -> off
	// "true" -> medium
	// "low" -> low
	// "High" -> high
}

// ExampleCapOutputTokens shows the output-token capping semantics: a zero
// ceiling defaults to OutputTokenMax, and an unknown model limit (<= 0) returns
// the ceiling.
func ExampleCapOutputTokens() {
	fmt.Println(chatmodel.CapOutputTokens(8_000, 16_000))  // min(8000, 16000)
	fmt.Println(chatmodel.CapOutputTokens(50_000, 16_000)) // min(50000, 16000)
	fmt.Println(chatmodel.CapOutputTokens(0, 16_000))      // unknown limit -> ceiling
	fmt.Println(chatmodel.CapOutputTokens(100_000, 0))     // ceiling default
	// Output:
	// 8000
	// 16000
	// 16000
	// 32000
}

// ExampleNew shows constructing a model.ToolCallingChatModel for an
// OpenAI-compatible endpoint with a thinking level and an output-token cap. The
// result can be further decorated, e.g. with cachestab.NewToolCallingChatModel.
func ExampleNew() {
	ctx := context.Background()

	m, err := chatmodel.New(ctx, &chatmodel.Config{
		Plan:            "openai",
		BaseURL:         "http://localhost:0",
		Model:           "gpt-x",
		Temperature:     0.7,
		Thinking:        chatmodel.High,
		MaxOutputTokens: chatmodel.CapOutputTokens(0, 0),
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(m != nil)
	// Output:
	// true
}
