package log

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func setupLogger() (*logrus.Logger, *bytes.Buffer) {
	logger := logrus.New()
	buf := &bytes.Buffer{}
	logger.SetOutput(buf)
	logger.SetLevel(logrus.TraceLevel)
	logger.SetFormatter(&logrus.JSONFormatter{})
	return logger, buf
}

func TestHandlerOnStartChatModel(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Name:      "my-chat",
		Type:      "openai/gpt-4o",
		Component: components.ComponentOfChatModel,
	}
	input := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.User, Content: "hello"},
		},
	}

	_ = h.OnStart(context.Background(), info, input)

	output := buf.String()
	if !strings.Contains(output, `"component":"ChatModel"`) {
		t.Errorf("expected component ChatModel in output: %s", output)
	}
	if !strings.Contains(output, `"component_name":"my-chat"`) {
		t.Errorf("expected component_name my-chat in output: %s", output)
	}
	if !strings.Contains(output, `"component_type":"openai/gpt-4o"`) {
		t.Errorf("expected component_type openai/gpt-4o in output: %s", output)
	}
	if !strings.Contains(output, `"messages":1`) {
		t.Errorf("expected messages=1 in output: %s", output)
	}
	if !strings.Contains(output, `"msg":"chat model start"`) {
		t.Errorf("expected chat model start message: %s", output)
	}
}

func TestHandlerOnEndChatModel(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Name:      "my-chat",
		Type:      "openai/gpt-4o",
		Component: components.ComponentOfChatModel,
	}
	output := &model.CallbackOutput{
		Message: &schema.Message{
			Role:    schema.Assistant,
			Content: "Hello, how can I help?",
		},
		TokenUsage: &model.TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	_ = h.OnEnd(context.Background(), info, output)

	out := buf.String()
	if !strings.Contains(out, `"component":"ChatModel"`) {
		t.Errorf("expected component ChatModel: %s", out)
	}
	if !strings.Contains(out, `"content":"Hello, how can I help?"`) {
		t.Errorf("expected content: %s", out)
	}
	if !strings.Contains(out, `"prompt_tokens":10`) {
		t.Errorf("expected prompt_tokens=10: %s", out)
	}
	if !strings.Contains(out, `"completion_tokens":5`) {
		t.Errorf("expected completion_tokens=5: %s", out)
	}
	if !strings.Contains(out, `"total_tokens":15`) {
		t.Errorf("expected total_tokens=15: %s", out)
	}
	if !strings.Contains(out, `"msg":"chat model end"`) {
		t.Errorf("expected chat model end message: %s", out)
	}
}

func TestHandlerChatModelWithReasoning(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "anthropic/claude-sonnet-4-20250514",
	}
	output := &model.CallbackOutput{
		Message: &schema.Message{
			Role:             schema.Assistant,
			Content:          "The answer is 42.",
			ReasoningContent: "Let me think about this carefully...",
		},
	}

	h.OnEnd(context.Background(), info, output)

	out := buf.String()
	if !strings.Contains(out, `"reasoning":"Let me think about this carefully..."`) {
		t.Errorf("expected reasoning content: %s", out)
	}
}

func TestHandlerOnStartTool(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Name:      "get_weather",
		Type:      "weather_api",
		Component: components.ComponentOfTool,
	}
	input := &tool.CallbackInput{
		ArgumentsInJSON: `{"city": "Paris"}`,
	}

	h.OnStart(context.Background(), info, input)

	out := buf.String()
	if !strings.Contains(out, `"component":"Tool"`) {
		t.Errorf("expected component Tool: %s", out)
	}
	if !strings.Contains(out, `"component_name":"get_weather"`) {
		t.Errorf("expected component_name get_weather: %s", out)
	}
	if !strings.Contains(out, `"input":"{\"city\": \"Paris\"}"`) {
		t.Errorf("expected tool input: %s", out)
	}
	if !strings.Contains(out, `"msg":"tool start"`) {
		t.Errorf("expected tool start message: %s", out)
	}
}

func TestHandlerOnEndTool(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Name:      "get_weather",
		Type:      "weather_api",
		Component: components.ComponentOfTool,
	}
	output := &tool.CallbackOutput{
		Response: "Sunny, 22°C",
	}

	h.OnEnd(context.Background(), info, output)

	out := buf.String()
	if !strings.Contains(out, `"output":"Sunny, 22°C"`) {
		t.Errorf("expected tool output: %s", out)
	}
	if !strings.Contains(out, `"msg":"tool end"`) {
		t.Errorf("expected tool end message: %s", out)
	}
}

func TestHandlerOnError(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}
	err := io.ErrUnexpectedEOF

	h.OnError(context.Background(), info, err)

	out := buf.String()
	if !strings.Contains(out, `"error":"unexpected EOF"`) {
		t.Errorf("expected error message: %s", out)
	}
	if !strings.Contains(out, `"level":"debug"`) {
		t.Errorf("expected debug level: %s", out)
	}
}

func TestHandlerDefaultComponent(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	t.Run("start", func(t *testing.T) {
		buf.Reset()
		info := &callbacks.RunInfo{
			Name:      "my-indexer",
			Type:      "opensearch",
			Component: components.ComponentOfIndexer,
		}
		h.OnStart(context.Background(), info, nil)

		out := buf.String()
		if !strings.Contains(out, `"component":"Indexer"`) {
			t.Errorf("expected component Indexer: %s", out)
		}
		if !strings.Contains(out, `"msg":"component start"`) {
			t.Errorf("expected generic start message: %s", out)
		}
	})

	t.Run("end", func(t *testing.T) {
		buf.Reset()
		info := &callbacks.RunInfo{
			Component: components.ComponentOfRetriever,
		}
		h.OnEnd(context.Background(), info, nil)

		out := buf.String()
		if !strings.Contains(out, `"component":"Retriever"`) {
			t.Errorf("expected component Retriever: %s", out)
		}
		if !strings.Contains(out, `"msg":"component end"`) {
			t.Errorf("expected generic end message: %s", out)
		}
	})
}

func TestHandlerAgentField(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	ctx := activity.WithAgent(context.Background(), "supervisor")
	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}
	input := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.User, Content: "hello"},
		},
	}

	h.OnStart(ctx, info, input)

	out := buf.String()
	if !strings.Contains(out, `"agent":"supervisor"`) {
		t.Errorf("expected agent field 'supervisor': %s", out)
	}
}

func TestHandlerOnStartWithStreamInput(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Name:      "get_weather",
		Type:      "weather_api",
		Component: components.ComponentOfTool,
	}

	input := schema.StreamReaderFromArray([]callbacks.CallbackInput{
		&tool.CallbackInput{ArgumentsInJSON: `{"city":"Par`},
		&tool.CallbackInput{ArgumentsInJSON: `is"}`},
	})

	ctx, done := withDoneChan(context.Background())
	_ = h.OnStartWithStreamInput(ctx, info, input)
	<-done

	out := buf.String()
	if !strings.Contains(out, `"input":"{\"city\":\"Paris\"}"`) {
		t.Errorf("expected stream input '{\"city\":\"Paris\"}': %s", out)
	}
	if !strings.Contains(out, `"msg":"tool stream input end"`) {
		t.Errorf("expected 'tool stream input end' msg: %s", out)
	}
}

func TestHandlerOnEndWithStreamOutput(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
		Type:      "openai/gpt-4o",
	}

	output := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		&model.CallbackOutput{Message: &schema.Message{Content: "Hello"}},
		&model.CallbackOutput{Message: &schema.Message{Content: " world"}},
		&model.CallbackOutput{TokenUsage: &model.TokenUsage{
			PromptTokens:     5,
			CompletionTokens: 2,
			TotalTokens:      7,
		}},
	})

	ctx, done := withDoneChan(context.Background())
	h.OnEndWithStreamOutput(ctx, info, output)
	<-done

	out := buf.String()
	if !strings.Contains(out, `"content":"Hello world"`) {
		t.Errorf("expected stream content 'Hello world': %s", out)
	}
	if !strings.Contains(out, `"prompt_tokens":5`) {
		t.Errorf("expected prompt_tokens=5: %s", out)
	}
	if !strings.Contains(out, `"msg":"chat model stream end"`) {
		t.Errorf("expected 'chat model stream end' msg: %s", out)
	}
}

func TestHandlerStreamInputNonTool(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Component: components.ComponentOfChatModel,
	}
	input := schema.StreamReaderFromArray([]callbacks.CallbackInput{
		"ignored",
	})

	// Should close the stream without logging anything
	h.OnStartWithStreamInput(context.Background(), info, input)

	// Stream should still be readable after close (close is no-op on array reader)
	_, err := input.Recv()
	if err != nil {
		t.Errorf("expected stream to still be readable after close, got err: %v", err)
	}

	// No log output expected for non-tool components
	out := buf.String()
	if out != "" {
		t.Errorf("expected no log output for non-tool stream input, got: %s", out)
	}
}

func TestHandlerStreamOutputNonChatModel(t *testing.T) {
	logger, buf := setupLogger()
	h := NewHandler(logrus.NewEntry(logger))

	info := &callbacks.RunInfo{
		Component: components.ComponentOfTool,
	}
	output := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		"ignored",
	})

	h.OnEndWithStreamOutput(context.Background(), info, output)

	_, err := output.Recv()
	if err != nil {
		t.Errorf("expected stream to still be readable after close, got err: %v", err)
	}

	out := buf.String()
	if out != "" {
		t.Errorf("expected no log output for non-chatmodel stream output, got: %s", out)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "short",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "exact",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "long",
			input:  "this is a long string",
			maxLen: 6,
			want:   "this i…[truncated 21 total chars]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("truncate mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
