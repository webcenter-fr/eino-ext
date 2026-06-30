package cachestab_test

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/components/middleware/cachestab"
)

// demoModel is a minimal, provider-agnostic stand-in for a real
// model.ToolCallingChatModel (such as an OpenAI or Claude chat model) so the
// example stays self-contained and runnable.
type demoModel struct{}

func newDemoModel() model.ToolCallingChatModel { return &demoModel{} }

func (d *demoModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (d *demoModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (d *demoModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return d, nil
}

// tool builds a ToolInfo whose parameters are intentionally declared in a
// non-alphabetical order to demonstrate the deterministic key sorting.
func tool(name string) *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: name,
		Desc: "demo tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"zebra": {Type: schema.String, Required: true},
			"alpha": {Type: schema.Number, Required: true},
			"mid":   {Type: schema.String},
		}),
	}
}

// ExampleNormalizeTools shows the standalone normalization helper. Tools are
// returned sorted by name, with each schema's keys deterministically ordered,
// which maximizes prompt-cache hit rates without changing semantics.
func ExampleNormalizeTools() {
	tools := []*schema.ToolInfo{tool("charlie"), tool("alpha"), tool("bravo")}

	normalized, err := cachestab.NormalizeTools(tools)
	if err != nil {
		panic(err)
	}

	for _, t := range normalized {
		fmt.Println(t.Name)
	}
	// Output:
	// alpha
	// bravo
	// charlie
}

// ExampleNewToolCallingChatModel shows how to wrap any
// model.ToolCallingChatModel (e.g. an OpenAI or Claude chat model) so that every
// WithTools call receives normalized, deterministically-ordered tool
// definitions. The wrapper is transparent: Generate and Stream are delegated
// unchanged, and only the tool definitions are normalized.
func ExampleNewToolCallingChatModel() {
	// base is any model.ToolCallingChatModel, for example:
	//
	//   base, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{ /* ... */ })
	//   if err != nil {
	//       return err
	//   }
	//
	// Here we keep the example self-contained and provider-agnostic by
	// pretending `base` is already constructed.
	var base = newDemoModel()

	m, err := cachestab.NewToolCallingChatModel(base)
	if err != nil {
		panic(err)
	}

	// Tools arrive at the underlying model normalized and sorted, improving
	// prompt-cache stability across calls.
	withTools, err := m.WithTools([]*schema.ToolInfo{tool("charlie"), tool("alpha")})
	if err != nil {
		panic(err)
	}

	fmt.Println(withTools != nil)
	// Output:
	// true
}
