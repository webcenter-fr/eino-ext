package contextopt

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel is a minimal model.BaseChatModel for tests.
type fakeModel struct {
	lastInput []*schema.Message
	reply     string
}

func (f *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.lastInput = input
	return schema.AssistantMessage(f.reply, nil), nil
}

func (f *fakeModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.lastInput = input
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(schema.AssistantMessage(f.reply, nil), nil)
	sw.Close()
	return sr, nil
}

func TestNewModelSummarizerPrompt(t *testing.T) {
	fm := &fakeModel{reply: "RESULT"}
	s := NewModelSummarizer(fm)

	out, err := s.Summarize(context.Background(), []*schema.Message{user("hello")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "RESULT" {
		t.Fatalf("got %q", out)
	}
	prompt := fm.lastInput[0].Content
	if !strings.Contains(prompt, "## Goal") {
		t.Fatal("prompt missing template")
	}
	if strings.Contains(prompt, "<previous-summary>") {
		t.Fatal("unexpected previous-summary block")
	}
	if !strings.Contains(prompt, "Create a new anchored summary") {
		t.Fatal("missing new-summary instruction")
	}
}

func TestNewModelSummarizerWithPreviousSummary(t *testing.T) {
	fm := &fakeModel{reply: "RESULT"}
	s := NewModelSummarizer(fm)
	if _, err := s.Summarize(context.Background(), []*schema.Message{user("hi")}, "PREVIOUS"); err != nil {
		t.Fatal(err)
	}
	prompt := fm.lastInput[0].Content
	if !strings.Contains(prompt, "<previous-summary>\nPREVIOUS\n</previous-summary>") {
		t.Fatalf("missing previous-summary block: %q", prompt)
	}
}

func TestModelSummarizerTruncatesToolOutput(t *testing.T) {
	fm := &fakeModel{reply: "RESULT"}
	s := NewModelSummarizer(fm, WithToolOutputMaxChars(10))
	history := []*schema.Message{toolMsg("c1", strings.Repeat("z", 100))}
	if _, err := s.Summarize(context.Background(), history, ""); err != nil {
		t.Fatal(err)
	}
	prompt := fm.lastInput[0].Content
	if strings.Count(prompt, "z") > 20 {
		t.Fatalf("tool output not truncated: %q", prompt)
	}
	if !strings.Contains(prompt, "[output truncated]") {
		t.Fatal("missing truncation marker")
	}
}
